#!/usr/bin/env python3
"""Import waifu-public.sql into a Postgres/Supabase database.

Two gotchas this handles for you:
  1. The dump opens with a \\restrict line (a pg_dump 18.6 hardening feature).
     psql older than 18 errors on the unknown backslash command, so the dump
     is streamed through a filter that drops \\restrict / \\unrestrict.
  2. psql exits 0 on error unless ON_ERROR_STOP=1 is set, which silently
     gives you a half-imported database. Everything here forces it on.

Usage:
    python3 import_supabase.py --check    # validate connection, nothing written
    python3 import_supabase.py            # do the import
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

from loadenv import die, load_env

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1] if HERE.parent.name == "mirror" else HERE.parent


def _find(name):
    """Search up from this file for a data file that lives outside the repo."""
    for base in HERE.parents:
        hit = base / name
        if hit.exists():
            return hit
    return REPO / name
DEFAULT_SQL = _find("waifu-public.sql")

STRIP_RE = re.compile(rb"^\\(restrict|unrestrict)\b")


def psql_cmd(database_url):
    """psql wrapper that never prints the password."""
    if shutil.which("psql") is None:
        die(
            "psql not found on PATH.\n"
            "  Debian/Ubuntu : sudo apt install postgresql-client\n"
            "  Fedora        : sudo dnf install postgresql\n"
            "  macOS         : brew install libpq && PATH=\"$PATH\"/opt/homebrew/bin\n"
            "  Or use the Supabase dashboard SQL editor instead."
        )
    env = dict(os.environ)
    env["PGPASSWORD"] = ""
    env["PGCONNECT_TIMEOUT"] = "15"
    return [
        "psql",
        database_url,
        "-X",  # ignore ~/.psqlrc so behaviour is reproducible
        "-v",
        "ON_ERROR_STOP=1",
        "-q",
    ], env


def run(args, env, input_bytes=None, capture=True):
    return subprocess.run(
        args,
        env=env,
        input=input_bytes,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE,
        check=False,
    )


def stream_filtered(sql_path):
    """Yield the dump with \\restrict lines removed."""
    with open(sql_path, "rb") as fh:
        for line in fh:
            if STRIP_RE.match(line):
                continue
            yield line


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--sql", type=Path, default=DEFAULT_SQL)
    ap.add_argument("--check", action="store_true", help="test connection only")
    ap.add_argument(
        "--skip-counts", action="store_true", help="do not run the final COUNT query"
    )
    args = ap.parse_args()

    env = load_env(required=("DATABASE_URL",))
    database_url = env["DATABASE_URL"]

    if not args.sql.exists():
        die(f"SQL dump not found: {args.sql}")

    print(f"dump  : {args.sql} ({args.sql.stat().st_size / 1e6:.1f} MB)")
    print(f"target: {_redact(database_url)}")

    psql, penv = psql_cmd(database_url)

    # 1. Connection + server version.
    probe = run(
        psql
        + [
            "-c",
            "SELECT version();",
            "-c",
            "SELECT extname FROM pg_extension ORDER BY 1;",
            "-c",
            "SELECT count(*) FROM information_schema.tables "
            "WHERE table_schema='public';",
        ],
        penv,
    )
    out = (probe.stdout or b"").decode("utf-8", "replace")
    err = (probe.stderr or b"").decode("utf-8", "replace")
    if probe.returncode != 0:
        die(f"psql could not connect:\n{err.strip()}")

    tables_now = _last_number(out)
    print("\n--- server ---")
    print(out.strip())
    print(f"public tables currently: {tables_now}")

    if tables_now and tables_now > 0:
        print(
            "\nWARNING: the database already has tables. Importing on top of a\n"
            "non-empty database can conflict. Drop it first if unsure."
        )

    if args.check:
        print("\n--check only. Nothing was written.")
        return

    # 2. Extensions. Supabase may have them disabled.
    print("\n--- extensions ---")
    for ext in ("pgcrypto", "vector"):
        res = run(psql + ["-c", f"CREATE EXTENSION IF NOT EXISTS {ext};"], penv)
        msg = (res.stderr or b"").decode("utf-8", "replace").strip()
        if res.returncode == 0:
            print(f"{ext}: ok")
        else:
            print(f"{ext}: FAILED - {msg}")
            print(f"  enable it manually in the Supabase dashboard, then re-run.")

    # 3. The import itself.
    print("\n--- import ---")
    cmd = psql + ["-f", "-"]
    proc = subprocess.Popen(
        cmd,
        env=penv,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    written = 0
    try:
        for line in stream_filtered(args.sql):
            proc.stdin.write(line)
            written += 1
        proc.stdin.close()
    except BrokenPipeError:
        # psql exited early, almost certainly on a SQL error. Fall through to
        # communicate() so the real message is reported rather than a traceback.
        pass
    stdout, stderr = proc.communicate()

    if proc.returncode != 0:
        die(
            "Import failed:\n"
            + (stderr or b"").decode("utf-8", "replace").strip()[-3000:]
        )

    print(f"sent {written} lines, exit 0")

    # 4. Verify. This is the step that tells you it worked.
    if args.skip_counts:
        return

    print("\n--- verify ---")
    verify = run(
        psql
        + [
            "-c",
            'SELECT count(*) AS images FROM "Images";',
            "-c",
            'SELECT count(*) AS tags FROM "Tags";',
            "-c",
            'SELECT count(*) AS artists FROM "Artists";',
            "-c",
            'SELECT count(*) AS imagetags FROM "ImageTag";',
            "-c",
            'SELECT count(*) AS artistimage FROM "ArtistImage";',
            "-c",
            'SELECT "MigrationId" FROM "__EFMigrationsHistory" ORDER BY 1;',
        ],
        penv,
    )
    text = (verify.stdout or b"").decode("utf-8", "replace")
    if verify.returncode != 0:
        die(
            "verification query failed:\n"
            + (verify.stderr or b"").decode("utf-8", "replace").strip()[-2000:]
        )
    print(text.strip())

    images = _first_count(text)
    if images is None:
        print("\nCould not read the image count. Check the output above.")
    elif images == 4279:
        print(f"\nOK: {images} images imported. This matches the dump.")
    else:
        print(
            f"\nMISMATCH: got {images} images, expected 4279.\n"
            f"Re-run from a clean database if this looks wrong."
        )


def _first_count(text):
    # psql puts the column name and value on separate lines, so accept both
    # the row format (images | 4279) and the label/newline/value layout.
    m = re.search(r"\bimages\b\s*(?:\|\s*|\n\s*-+.+\n\s*)(\d+)", text)
    if not m:
        m = re.search(r"\bimages\b[\s\S]{0,80}?\n\s*(\d+)\s*\n", text)
    return int(m.group(1)) if m else None


def _last_number(text):
    nums = re.findall(r"^\s*(\d+)\s*$", text, re.M)
    return int(nums[-1]) if nums else 0


def _redact(url):
    return re.sub(r"://([^:/]+):[^@]+@", r"://\1:***@", url)


if __name__ == "__main__":
    main()
