#!/usr/bin/env python3
"""Convert a postgres:// URI to the keyword format Npgsql + Render want.
Prints the exact line to paste into Render's environment, with SSL forced on.

Usage:  uv run mkconn.py 'postgresql://user:pass@host:6543/db'
        uv run mkconn.py --env     # convert DATABASE_URL from .env instead
"""
import sys
from urllib.parse import urlparse, quote

DEFAULT_URI = "postgresql://postgres.REF:PASSWORD@POOLER:6543/postgres"


def convert(uri: str) -> str:
    u = urlparse(uri)
    if not u.hostname:
        raise SystemExit("error: could not parse that URI")
    parts = [
        f"Host={u.hostname}",
        f"Port={u.port or 5432}",
        f"Database={u.path.lstrip('/') or 'postgres'}",
        f"Username={u.username or 'postgres'}",
        f"Password={quote(u.password or '', safe='')}",
        "SSL Mode=Require",
    ]
    return "ConnectionStrings__DefaultConnection=" + ";".join(parts)


if __name__ == "__main__":
    if "--env" in sys.argv:
        sys.path.insert(0, ".")
        from loadenv import load_env

        uri = load_env(required=("DATABASE_URL",))["DATABASE_URL"]
    elif len(sys.argv) > 1:
        uri = sys.argv[1]
    else:
        uri = DEFAULT_URI
    print(convert(uri))
