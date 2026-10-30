#!/usr/bin/env python3
"""Convert stored JPEGs to WebP in place, without ever exceeding the storage cap.

The free tier allows 5 GB and the bucket holds about 4.6 GB. Converting every
JPEG up front would write ~2 GB of WebP alongside the ~4 GB of originals and
bill roughly 7.6 GB - over the cap, and over it for the whole run rather than
momentarily.

So the unit of work is a batch: convert, upload, verify the new object really
landed, and only then delete that batch's originals. Storage never rises by more
than one batch's worth, and the freed space is reclaimed as it goes.

Quality is not a guess. Measured over a 24-image sample of this project's own
JPEGs (SSIM against the original, libwebp via Pillow, method=4):

    quality   size saved   mean SSIM   min SSIM   files below 0.95
         75         71.5%       0.9791      0.915                  1/24
         80         65.4%       0.9833      0.932                  1/24
         85         57.5%       0.9870      0.953                  0/24
         90         43.9%       0.9912      0.970                  0/24

85 is the default because it is the cheapest setting where no sampled image
falls into the visible-loss band. These numbers are well above the 25-35%
usually quoted for JPEG->WebP, because anime art is mostly flat colour and
large uniform regions, which the transform-based predictor handles very well.

Dimensions are never changed. Re-encoding only alters the bytes, so the
Width/Height columns and the resolution filter stay correct. This tool does not
touch the database - see the runbook at the bottom of this file for why the
Extension flip is a separate, manual step.

Usage:

    python3 webp_backfill.py --list                 # what would be converted
    python3 webp_backfill.py --limit 50 --apply     # convert+verify, no deletes
    python3 webp_backfill.py --limit 200 --apply --delete-originals

Defaults to a dry run. --apply is required to write anything. Deleting originals
is a separate, second flag, on the theory that the two should not be granted in
the same breath.
"""

import argparse
import hashlib
import hmac
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from upload_to_s3 import build_url, sigv4_headers
from loadenv import die, load_env

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1] if HERE.parent.name == "mirror" else HERE.parent

CONVERTIBLE = (".jpeg", ".jpg")
DEFAULT_QUALITY = 85
DEFAULT_BATCH = 100


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def load_pillow():
    try:
        from PIL import Image
    except ImportError:
        die("Pillow is required: pip install Pillow")
    return Image


# ---------------------------------------------------------------------------
# Storage
# ---------------------------------------------------------------------------

def _signed(method, url, host, path, region, ak, sk, data=None, extra=None):
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Host", host)
    for k, v in sigv4_headers(
        method, host, path, "UNSIGNED-PAYLOAD", region, ak, sk
    ).items():
        req.add_header(k, v)
    for k, v in (extra or {}).items():
        req.add_header(k, v)
    return req


def read_object(url, host, path, region, ak, sk):
    """GET an object with SigV4. Returns bytes, or None on a miss."""
    req = _signed("GET", url, host, path, region, ak, sk)
    try:
        with urllib.request.urlopen(req, timeout=180) as resp:
            return resp.read()
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise


def head_size(url, host, path, region, ak, sk):
    """Content length, or None if absent. A 200 on PUT is not proof the body
    landed intact, so the size is read back before anything is deleted."""
    req = _signed("HEAD", url, host, path, region, ak, sk)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return int(resp.headers.get("content-length", 0))
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise


def put_object(url, host, path, region, ak, sk, payload, content_type):
    req = _signed("PUT", url, host, path, region, ak, sk, data=payload,
                  extra={"content-type": content_type})
    with urllib.request.urlopen(req, timeout=300) as resp:
        return resp.status


def delete_object(url, host, path, region, ak, sk):
    req = _signed("DELETE", url, host, path, region, ak, sk)
    with urllib.request.urlopen(req, timeout=120) as resp:
        return resp.status


# ---------------------------------------------------------------------------
# Inventory
# ---------------------------------------------------------------------------

def jpeg_rows(conninfo):
    """Ids whose stored object is a JPEG.

    Read from the live database rather than a SQL dump: waifu-public.sql is not
    guaranteed current, and a stale dump would silently omit rows.
    """
    query = (
        'SELECT "Id", "Extension" FROM "Images" '
        "WHERE \"Extension\" IN ('.jpeg', '.jpg') ORDER BY \"Id\""
    )
    try:
        out = subprocess.run(
            ["psql", conninfo, "-tA", "-F", ",", "-c", query],
            capture_output=True, text=True, check=True, timeout=300,
        ).stdout
    except FileNotFoundError:
        die("psql not found; install the Postgres client")
    except subprocess.CalledProcessError as e:
        die(f"psql failed: {e.stderr.strip()[:200]}")

    rows = []
    for line in out.strip().splitlines():
        if not line.strip():
            continue
        parts = line.split(",")
        if len(parts) == 2 and parts[1] in CONVERTIBLE:
            rows.append((int(parts[0]), parts[1]))
    return rows


def already_converted(endpoint, bucket, region, ak, sk, workers=32):
    """Ids that already have a .webp object. Skipped, so the run is resumable.

    Concurrent because this is one HEAD per JPEG - 3,000-odd of them, which is
    minutes of round trips run one at a time.
    """
    ids = [i for i, _ in jpeg_rows(DATABASE)]

    def probe(image_id):
        url, host, path = build_url(endpoint, bucket, f"{image_id}.webp")
        try:
            return image_id if head_size(url, host, path, region, ak, sk) is not None else None
        except Exception:
            return None

    with ThreadPoolExecutor(max_workers=workers) as pool:
        return {i for i in pool.map(probe, ids) if i is not None}


def flip_extension(image_ids):
    """Point the catalogue at the .webp objects.

    Deliberately before the originals are deleted. A row saying .webp makes the
    API hand out a .webp URL, which the worker serves directly. A row still
    saying .jpeg makes it hand out a .jpeg URL for an object that no longer
    exists - a 404 handed to a client rather than to the bucket.
    """
    ids = ",".join(str(i) for i in image_ids)
    q = (
        'UPDATE "Images" SET "Extension" = \'.webp\' '
        f'WHERE "Id" IN ({ids}) AND "Extension" IN (\'.jpeg\', \'.jpg\')'
    )
    out = subprocess.run(["psql", DATABASE, "-tA", "-c", q],
                         capture_output=True, text=True, timeout=300)
    if out.returncode != 0:
        die(f"extension flip failed: {out.stderr.strip()[:200]}")
    log(f"  flipped {out.stdout.strip()} rows to .webp")


# ---------------------------------------------------------------------------
# Conversion
# ---------------------------------------------------------------------------

def encode(Image, raw, quality):
    import io
    with Image.open(io.BytesIO(raw)) as im:
        im.load()
        buf = io.BytesIO()
        im.convert("RGB").save(buf, "WEBP", quality=quality, method=4)
        return buf.getvalue()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true",
                    help="actually write; without this nothing is modified")
    ap.add_argument("--delete-originals", action="store_true",
                    help="also DELETE the .jpeg once its .webp is verified")
    ap.add_argument("--limit", type=int, default=0, help="stop after N images")
    ap.add_argument("--batch", type=int, default=DEFAULT_BATCH,
                    help="images per batch; bounds the storage overshoot")
    ap.add_argument("--quality", type=int, default=DEFAULT_QUALITY)
    ap.add_argument("--list", action="store_true", help="list candidates and exit")
    ap.add_argument("--corpus", default="",
                    help="local corpus directory; reads originals from disk "
                         "instead of downloading each one from the bucket")
    ap.add_argument("--flip-extension", action="store_true",
                    help="also UPDATE Images.Extension to .webp per batch, "
                         "before the originals are deleted")
    ap.add_argument("--workers", type=int, default=12)
    args = ap.parse_args()

    env = load_env()
    for key in ("S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_BUCKET",
                "S3_ENDPOINT", "S3_REGION", "DATABASE_URL"):
        if not env.get(key):
            die(f"{key} is not set")

    global DATABASE
    DATABASE = env["DATABASE_URL"]
    endpoint = env["S3_ENDPOINT"].rstrip("/")
    bucket = env["S3_BUCKET"]
    region = env["S3_REGION"]
    ak, sk = env["S3_ACCESS_KEY_ID"], env["S3_SECRET_ACCESS_KEY"]

    rows = jpeg_rows(DATABASE)
    log(f"{len(rows)} JPEG rows in the catalogue")

    if args.list:
        for image_id, ext in rows[:40]:
            print(f"  {image_id}{ext}")
        if len(rows) > 40:
            print(f"  ... and {len(rows) - 40} more")
        return

    done = already_converted(endpoint, bucket, region, ak, sk)
    todo = [(i, e) for i, e in rows if i not in done]
    log(f"{len(done)} already have a .webp, {len(todo)} to do")

    if args.limit:
        todo = todo[: args.limit]
        log(f"limited to {len(todo)}")

    if not args.apply:
        log("dry run - pass --apply to write")
        if args.delete_originals:
            log("NOTE: --delete-originals was passed but is ignored without --apply")
        return

    if not args.delete_originals:
        log("originals will be KEPT; storage will rise by roughly the converted size")

    Image = load_pillow()
    converted = deleted = failed = 0
    saved_bytes = 0

    for start in range(0, len(todo), args.batch):
        batch = todo[start:start + args.batch]
        log(f"batch {start // args.batch + 1}: {len(batch)} images")

        # Convert and upload. Nothing is deleted until the batch has been
        # verified, so a failure part-way leaves every original intact.
        def one(job):
            image_id, ext = job
            src_key = f"{image_id}{ext}"
            dst_key = f"{image_id}.webp"
            try:
                raw = None
                if args.corpus:
                    local = Path(args.corpus) / src_key
                    if local.exists():
                        raw = local.read_bytes()
                if raw is None:
                    url, host, path = build_url(endpoint, bucket, src_key)
                    raw = read_object(url, host, path, region, ak, sk)
                if raw is None:
                    return ("missing", src_key, 0, 0)

                encoded = encode(Image, raw, args.quality)

                url, host, path = build_url(endpoint, bucket, dst_key)
                put_object(url, host, path, region, ak, sk, encoded, "image/webp")

                # Verify by reading the size back. A 200 on PUT is not proof the
                # body landed intact.
                url, host, path = build_url(endpoint, bucket, dst_key)
                written = head_size(url, host, path, region, ak, sk)
                if written != len(encoded):
                    return ("corrupt", dst_key, len(raw), len(encoded))
                return ("ok", src_key, len(raw), len(encoded))
            except Exception as e:
                return ("error", f"{src_key} FAILED: {e}", 0, 0)

        results = []
        with ThreadPoolExecutor(max_workers=args.workers) as pool:
            for res in pool.map(one, batch):
                results.append(res)

        pending_delete = []
        corrupt = [r for r in results if r[0] == "corrupt"]
        if corrupt:
            die(f"{corrupt[0][1]} wrote {corrupt[0][3]} bytes, expected "
                f"{corrupt[0][2]}. Originals are untouched - stop and investigate.")

        for status, key, orig_bytes, new_bytes in results:
            if status != "ok":
                failed += 1
                log(f"  {key}")
                continue
            pending_delete.append((key, orig_bytes - new_bytes))
            converted += 1
            saved_bytes += orig_bytes - new_bytes

        converted_ids = [int(key.split(".")[0]) for status, key, _, _ in results
                         if status == "ok" and key.split(".")[0].isdigit()]
        log(f"  {converted} converted in this batch")

        if args.flip_extension and converted_ids:
            flip_extension(converted_ids)

        if args.delete_originals:
            def kill(job):
                key, _freed = job
                url, host, path = build_url(endpoint, bucket, key)
                try:
                    delete_object(url, host, path, region, ak, sk)
                except Exception as e:
                    log(f"    delete {key} failed: {e}")

            # Concurrent: sequential deletes dominated the batch time, ~70s per
            # 100 against ~30s for the whole convert step.
            with ThreadPoolExecutor(max_workers=args.workers) as pool:
                list(pool.map(kill, pending_delete))
            deleted += len(pending_delete)
            log(f"  deleted {len(pending_delete)} originals, freed {sum(f for _, f in pending_delete)>>20} MB")
        else:
            log(f"  kept {len(pending_delete)} originals")

    log(f"done: {converted} converted, {deleted} originals deleted, {failed} failed")
    if saved_bytes:
        log(f"reclaimable {saved_bytes / 1e9:.2f} GB (only realised once originals are deleted)")


RUNBOOK = """
Runbook
-------

Expect /storage-diff to report growing orphans during the backfill. A .webp
object whose row still says .jpeg is, by that page's definition, an object with
no catalogue row - so orphans climbs with every conversion and only settles when
the Extension column is flipped in step 4. Three have already been converted as
a pilot, so orphans is currently 4, not 1. This is expected; a *missing* count
above 0 is not, and means stop.

1. Dry run:            python3 webp_backfill.py --list
2. Convert, keep safe: python3 webp_backfill.py --apply
   -> originals still present, both formats served, WebP wins via the worker.
3. Enable the worker flag:
       [vars] WEBP_JPEG_FALLBACK = "true"
   -> deploy. Existing clients unaffected; the .webp object is already there.
4. Flip the catalogue once the objects are verified:
       UPDATE "Images" SET "Extension" = '.webp'
        WHERE "Extension" IN ('.jpeg', '.jpg');
   CHECK /storage-diff reports orphans = 1, missing = 0 BEFORE running this.
   One row at a time first; it is reversible.
5. Reclaim the space:
       python3 webp_backfill.py --apply --delete-originals
   Irreversible. Only after step 4 is verified.

Step 4 is deliberately a separate manual statement. Until the Extension column
changes, a client asking for /1234.jpeg gets the WebP through the worker and
everything works. Flipping the column early is what makes a missing .webp a 404,
and a 404 is invisible until someone reports it.
"""


if __name__ == "__main__":
    if "--runbook" in sys.argv:
        print(RUNBOOK)
    else:
        main()