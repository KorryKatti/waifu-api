#!/usr/bin/env python3
"""Load credentials from the sibling .env file.

Kept in one place so no script ever reads a secret from argv (visible in `ps`)
and so nothing has to be typed on the command line.
"""

import os
import sys
from pathlib import Path

ENV_PATH = Path(__file__).resolve().parent / ".env"


def load_env(required=()):
    """Parse .env into os.environ without clobbering real environment vars."""
    if not ENV_PATH.exists():
        die(
            f"No .env at {ENV_PATH}\n"
            f"  cp .env.example .env && chmod 600 .env, then fill it in."
        )

    for raw in ENV_PATH.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip("'").strip('"')
        os.environ.setdefault(key, value)

    missing = [k for k in required if not os.environ.get(k)]
    if missing:
        die(f"Missing in .env: {', '.join(missing)}")

    unset = [
        k
        for k in required
        if os.environ.get(k, "").strip().lower()
        in {"", "<your-account-id>", "<password>", "changeme"}
    ]
    if unset:
        die(f"Still placeholders in .env: {', '.join(unset)}")

    return os.environ


def die(message, code=1):
    sys.stderr.write(f"error: {message}\n")
    raise SystemExit(code)


if __name__ == "__main__":
    load_env(required=("S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"))
    print(".env parsed OK")