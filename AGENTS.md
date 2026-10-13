# AGENTS.md

Notes for agents and contributors working in this repo. These are the things
that have actually cost time, not generic advice.

## Build and test

**Always verify in Release, because Render publishes in Release.** Debug builds
and `dotnet test` pass in situations where the deploy breaks.

```bash
# What Render actually runs
cd backend
docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet publish WaifuApi.Web/WaifuApi.Web.csproj -c Release -o /app/publish \
  /p:UseAppHost=false /p:Version=0.0.0-local /p:OpenApiGenerateDocumentsOnBuild=false

# Test suite
docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/dotnet/sdk:10.0 dotnet test
```

There is no local `dotnet`; use the SDK container. It is the same SDK Render
builds with.

## The Go rewrite: validated, merged, not yet deployed

`backend/go` is a complete, tested replacement for the C# service. It is on
`main`. **Production still runs the C# service**, and switching it over is an
outstanding step, not a completed one.

### How it deploys: two fields in the Render dashboard

Render builds this service from source rather than pulling a published image, so
no CI image-publishing workflow is needed and none exists. To point it at Go:

| Field | From | To |
|---|---|---|
| Dockerfile Path | `backend/Dockerfile` | `backend/go/Dockerfile` |
| Docker Build Context Directory | `backend/` | `backend/go` |

Both are relative to the repo root. **Leave the hostname alone.** The Cloudflare
Worker in `scripts/mirror/worker` hardcodes
`API_ORIGIN = "https://waifu-api-031n.onrender.com"`, so a new Render service
would 404 every anonymous API read the moment it went live. Swapping the image
on the existing service keeps the hostname and changes nothing upstream of it.
`bot/.env.example` names the same host.

`docker-compose.yml` and `docker-compose.preprod.yml` build the Go backend the
same way, so local and production match.

### Two things safe to know about the switch

**The boot-time cleanup is harmless.** `background.runJob` runs each job once at
startup, and `StatsCleanup` issues `DeleteDailyStatsBefore` — which sounds
alarming and is not. Go uses `AddDate(-1, 0, 0)` and C# uses `AddYears(-1)`:
the same one-year cutoff, and `DailyStats` currently holds 7 rows, none older
than a year. It would delete nothing. Revisit if that table ever passes a year
of data.

**Duplicate detection is off until the switch.** `PerceptualHash` now holds Go
hashes, which a C# upload cannot match — distances land near 32 against a
threshold of 4. This is why the cutover is worth doing rather than skipping.

### The C# service stays

It is the behavioural oracle the Go tests compare against, and
`.github/workflows/docker-image.yml` keeps building it in Release. Removing
either removes the thing that makes the rewrite trustworthy. Do not repoint that
workflow.

Full record, including two production bugs this found and how they were
measured: `plans/cutover-validation-log.md`. Plan and reasoning:
`backend/go/internal/images/phash/MIGRATION.md`.

### Never point the Go service at the production database

`background.runJob` runs each cleanup job **once at startup**, before its daily
ticker, matching the C# behaviour. `StatsCleanup` is unconditional and issues
`DeleteDailyStatsBefore`. Booting Go against production deletes rows immediately.

### PNGs are stored as WebP

Every PNG is converted at upload and served back at its `.png` path by the
Worker. `Image.Extension` is still `.png`, so a plain `{id}{ext}` fetch misses
all 1,162 PNG rows and reports them absent. Use `storage.ObjectKeyCandidates`
rather than constructing the key yourself.

### `/storage-diff` had a delete button pointed at the wrong list

The same trap reached `ListCatalogueKeys`, which reconstructed `{id}{ext}` and
compared it against a bucket of `{id}.webp`. Every PNG row came back an orphan —
1,162 of them, plus one genuine orphan, which is the **1,163** the reconciliation
page showed on its first appearance under Go. The page renders a delete button
beside every orphan row.

Two directions were wrong, not one. A stored object is an orphan only when it
matches no row's candidate keys, and a row is missing only when *none* of its
keys are in the bucket. Testing each key independently flags a PNG as missing
while its `.webp` sits in the bucket.

So the page counts **orphans = 1, missing = 0** on a healthy bucket, and the one
orphan is `__parity_probe.tmp`, a file left behind by the route parity sweep.

Reconciliation code is destructive-adjacent. When it disagrees with reality,
believe it over the database until a probe proves otherwise, and never click
delete on a list whose construction you have not read.

### Duplicate detection has never worked, on any implementation

The C# hashes do not reproduce themselves, and v6 — which was Go — had no
duplicate detection at all. The threshold of `4` was chosen against an algorithm
that never existed consistently.

**It has now been measured against the real distribution, and `4` is correct.
Do not retune it.** Go's pHash reproduces itself, so the corpus is comparable
for the first time. Genuine duplicates sit at Hamming distance **0 and 2**;
there are **no pairs at 3–9**; the nearest genuinely-different images start at
**10**. Distance 4 has a wide empty margin on both sides.

The stale advice was the risk: a future reader would have retuned a validated
number against no data. If the corpus changes shape, re-measure — do not adjust
by feel.

### Known licensing trap

`SixLabors.ImageSharp` **4.x is commercially licensed** and fails Release
publish without a `SixLaborsLicenseKey`. Do not upgrade to it casually — the
project is deliberately pinned to **3.1.12**, which already ships
`SixLabors.ImageSharp.Formats.Webp.WebpEncoder`. Upgrading buys nothing and
breaks the deploy. 3.1.12 does carry open NuGet security advisories (NU1902 /
NU1903); that is a known, accepted trade-off pending a decision on either a
Six Labors license or replacing ImageSharp entirely.

## Storage and images

- Images live in a **private** bucket; there is no public read path. The
  Cloudflare Worker in `scripts/mirror/worker` signs SigV4 and caches at the
  edge. Backend code and this worker are what stand between you and a broken
  public site.
- Worker secrets are **not** in `wrangler.toml`. They are set with
  `npx wrangler secret put` and sourced from `scripts/mirror/.env`, which is
  gitignored.
- `Image.Extension` is the single source of truth for both the S3 key
  (`{id}{ext}`) and the URL the API hands out. If you change one path, check
  the other still agrees — a mismatch produces images that 404.
- The perceptual hash behind duplicate detection is computed from the
  **original** upload bytes, never from a re-encoded copy. Keep it that way;
  hashing a lossy re-encode makes dedupe unreliable across uploads.

## Data and migrations

- `waifu-public.sql`-style dumps are **not** guaranteed current. Reconcile
  counts against the live API and against the local mirror before trusting a
  dump as a manifest. A stale dump silently omits recent images.
- `Image.PerceptualHash` is a Postgres `bit(64)`, which `pg_dump` renders as
  **64 binary characters**, not hex. The API exposes it as 16 lowercase hex
  chars via `BitArrayHelper.ToHex`. Mixing the two up silently breaks
  comparison.

### Perceptual hashes are not comparable across the two backends

The C# service does not reproduce its own hashes. Running the oracle twice on
one corpus with identical library versions, once normally and once with
`DOTNET_EnableHWIntrinsic=0`, gave **248/554 identical** hashes, with
divergences up to 38 bits — `ResizeKernel.ConvolveCore` accumulates with
`Vector256.MultiplyAddEstimate` when AVX2 is present and a scalar float loop
when it is not, and the two do not agree.

So the ~4,000 stored hashes are a **mixture of at least two algorithms**,
decided by whichever machine computed them. Consequences worth remembering:

- A hash computed by the Go backend will **not** match historical rows, so
  duplicate detection is blind across the boundary until a rehash backfill runs.
- The Hamming threshold of 4 was chosen assuming a stable hash. It needs
  re-validating after that backfill.
- `internal/images/phash` is intentionally *not* chasing bit-exactness. Further
  filter tuning cannot close a gap whose far side moves with the CPU. See
  `backend/go/tools/oracle/README.md` for the full findings.

## Verifying your own work

- Do not tell someone a command or a cache-buster works before reading the code
  that will run it.
- When a probe or script fails, suspect the probe first. Several "findings"
  during the Tigris migration were bugs in the measurement, not the system.
- Reconcile totals from independent sources (dashboard, live API, object
  store) before declaring a migration complete — that is how six missing images
  surfaced.

### Render's connection string is Npgsql format, not a URL

`ConnectionStrings__DefaultConnection` on Render is keyword/value with
**semicolon** separators, because that is what the .NET service reads and what
`scripts/mirror/mkconn.py` prints for pasting into the dashboard:

```
Host=db.example;Port=5432;Database=postgres;Username=u;Password=p%40ss;SSL Mode=Require
```

pgx does not accept that. `ParseConfig` understands a `postgres://` URL or
space-separated pairs, so a semicolon-separated string parses to *nothing* and
every field falls back to its default — the OS user, a blank database, and a
Unix socket at `/tmp`. The error that comes out says the database is
unreachable, not that the format is wrong:

```
failed to connect to `user=nonroot database=`: /tmp/.s.PGSQL.5432 ... no such file or directory
```

`internal/config.normalizeConnectionString` converts it, and handles three
things the obvious implementation misses: the password is percent-encoded by
`mkconn.py` and Npgsql decodes it, `SSL Mode` has a space in the key, and
`SSL Mode=Require` has a capital R that pgx rejects outright. URLs pass through
untouched.

If a connection failure ever reports `user=nonroot` or a socket path, this is
why — not an unreachable database.

### The Go service has no route-sweep blind spot left on CORS

`withCORS` was missing entirely from the first cutover and took the site down.
The C# service used `UseCors("AllowAll")` — any origin, any method, any header —
and the port dropped it.

The failure is hard to diagnose from the symptoms, because it looks like a
network problem rather than a missing header. Browsers refuse to read a
cross-origin response that carries no `Access-Control-Allow-Origin`, and a
preflight for anything with an `Authorization` header or a JSON body got a
**405**, because Go's mux matches a method exactly and no OPTIONS route was
registered. So the frontend saw a blocked request *and* an outright rejection,
for everything, while `curl` worked perfectly.

It is invisible to `routecheck` and to a 59-endpoint sweep, because a route is a
path and a method and CORS is a **header**. A route sweep will never catch this.
Header behaviour needs its own tests, which is what `cors_test.go` is.

### The test database is more permissive than production

`schema.sql` documents `DEFAULT 0` and `DEFAULT false` on columns like
`Users.RequestCount`. **Production has no defaults on any of them** — EF Core
created them bare, `NOT NULL` with nothing to fill in. Every Go INSERT must
therefore supply every `NOT NULL` column explicitly; omitting one inserts NULL
and fails at runtime.

This nearly caused a silent outage. `UpsertUserByDiscordID` omitted
`IsBlacklisted` and the three counters, so **every Discord login failed** with a
not-null violation, while the tests passed — because the test database is built
from `schema.sql`, which has the defaults the real database lacks. A test
database that differs from production defeats the point of having tests.

To find this class of bug, compare the live table against what the code writes,
not against `schema.sql`. Rolled-back transactions are the check:

```sql
BEGIN;
INSERT INTO "Users" ("DiscordId","Name") VALUES ('probe','probe') RETURNING "Id";
ROLLBACK;
```

Fourteen production INSERTs were checked this way; only `Users` was wrong.
