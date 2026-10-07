#!/usr/bin/env python3
"""Make the S3 bucket publicly readable (anonymous GetObject).

Applies a bucket policy granting s3:GetObject to Principal "*". Works on
IDrive e2, Backblaze B2, Tigris and AWS. Not supported by Cloudflare R2,
which uses a dashboard toggle instead.

Idempotent: safe to re-run. Use --check to show the current policy.
"""

import argparse
import hashlib
import hmac
import json
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone

from loadenv import die, load_env

PUBLIC_READ = {
    "Version": "2012-10-17",
    "Statement": [
        {
            "Sid": "PublicRead",
            "Effect": "Allow",
            "Principal": "*",
            "Action": ["s3:GetObject"],
            "Resource": [f"arn:aws:s3:::BUCKET/*"],
        }
    ],
}


def sign(key, msg):
    return hmac.new(key, msg.encode("utf-8"), hashlib.sha256).digest()


def call(method, host, path, query, body, region, access_key, secret_key):
    """One signed S3 request. `path` and `query` are separate because the
    canonical request needs the path alone and the query URI-encoded."""
    payload_hash = hashlib.sha256(body).hexdigest() if body is not None else "UNSIGNED-PAYLOAD"
    now = datetime.now(timezone.utc)
    amz_date = now.strftime("%Y%m%dT%H%M%SZ")
    date_stamp = now.strftime("%Y%m%d")

    signed = "host;x-amz-content-sha256;x-amz-date"
    canonical_headers = (
        f"host:{host}\nx-amz-content-sha256:{payload_hash}\nx-amz-date:{amz_date}\n"
    )
    canonical_request = "\n".join(
        [method, path, query, canonical_headers, signed, payload_hash]
    )
    scope = f"{date_stamp}/{region}/s3/aws4_request"
    string_to_sign = "\n".join(
        [
            "AWS4-HMAC-SHA256",
            amz_date,
            scope,
            hashlib.sha256(canonical_request.encode()).hexdigest(),
        ]
    )
    k = sign(
        sign(sign(sign(f"AWS4{secret_key}".encode(), date_stamp), region), "s3"),
        "aws4_request",
    )
    signature = hmac.new(k, string_to_sign.encode(), hashlib.sha256).hexdigest()

    url = f"https://{host}{path}"
    if query:
        url = f"{url}?{query}"
    req = urllib.request.Request(url, data=body, method=method)
    req.add_header("Host", host)
    req.add_header("x-amz-date", amz_date)
    req.add_header("x-amz-content-sha256", payload_hash)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    req.add_header(
        "Authorization",
        f"AWS4-HMAC-SHA256 Credential={access_key}/{scope}, "
        f"SignedHeaders={signed}, Signature={signature}",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true", help="show policy, change nothing")
    ap.add_argument("--revoke", action="store_true", help="remove public access")
    args = ap.parse_args()

    env = load_env(
        required=("S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_BUCKET", "S3_ENDPOINT", "S3_REGION")
    )
    host = urllib.parse.urlparse(env["S3_ENDPOINT"]).netloc
    bucket = env["S3_BUCKET"]
    path = f"/{bucket}"

    print(f"bucket: {bucket}")
    print(f"host  : {host}")

    if args.revoke:
        status, body = call("DELETE", host, path, "", None, env["S3_REGION"],
                            env["S3_ACCESS_KEY_ID"], env["S3_SECRET_ACCESS_KEY"])
        print(f"revoke -> HTTP {status}")
        if body.strip():
            print(body[:500])
        return

    if args.check:
        status, body = call("GET", host, path, "policy", None, env["S3_REGION"],
                            env["S3_ACCESS_KEY_ID"], env["S3_SECRET_ACCESS_KEY"])
        print(f"\ncurrent policy -> HTTP {status}")
        print(body[:2000] if body.strip() else "(none)")
        return

    policy = json.loads(json.dumps(PUBLIC_READ).replace("BUCKET", bucket))
    body = json.dumps(policy).encode()
    status, resp = call("PUT", host, path, "policy", body, env["S3_REGION"],
                        env["S3_ACCESS_KEY_ID"], env["S3_SECRET_ACCESS_KEY"])
    print(f"set policy -> HTTP {status}")
    if status not in (200, 204):
        print(resp[:1000])
        sys.exit(1)
    print("anonymous s3:GetObject granted on", f"arn:aws:s3:::{bucket}/*")
    print("\nnow test:")
    print(f"  curl -sI https://{host}/{bucket}/1.png")


if __name__ == "__main__":
    main()