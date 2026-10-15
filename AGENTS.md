# AGENTS.md

Notes for agents and contributors working in this repo. These are the things
that have actually cost time, not generic advice.

**This file is public. Keep the rule, drop the confession.** Record what a
contributor needs in order to write correct code. Do not record the size of a
gap, the shape of a failure, or the number of records something went wrong for.
If a fix needs a detail that would help someone find the hole, that detail
belongs in the commit that closes it, not here — and even there, say what changed
rather than what was exploitable.

## Build and test

**The Go suite is the one that matters** — it is what production runs. The C#
suite exists because the Go tests compare against it.

```bash
# Go: needs a real database and refuses to run without one rather than
# silently skipping.
docker run -d --name waifu-testdb -p 55432:5432 \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=waifutest pgvector/pgvector:pg18
docker exec -i waifu-testdb psql -U postgres -d waifutest \
  < backend/go/internal/store/schema.sql
cd backend/go
WAIFU_TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/waifutest' \
  go test ./...
```

```bash
# C#: the behavioural oracle, and what Render used to publish
cd backend
docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet publish WaifuApi.Web/WaifuApi.Web.csproj -c Release -o /app/publish \
  /p:UseAppHost=false /p:Version=0.0.0-local /p:OpenApiGenerateDocumentsOnBuild=false
docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/dotnet/sdk:10.0 dotnet test
```

There is no local `dotnet`; use the SDK container. **Always verify C# in
Release** — Debug and `dotnet test` pass in situations where the deploy breaks.

## Where the backend actually runs

**Production is the Go service.** The cutover is done, not pending. Render
builds `backend/go/Dockerfile` with build context `backend/go`;
`docker-compose.yml` builds it the same way, so local and production match.

The C# service is **retained as the behavioural oracle** the Go tests compare
against, and `.github/workflows/docker-image.yml` keeps building it. Removing
either removes the thing that makes the rewrite trustworthy. Do not repoint that
workflow, and do not add features to it — development happens in `backend/go`.

**Leave the hostname alone.** The Worker hardcodes `API_ORIGIN`, so a *new*
Render service would break anonymous reads the moment it went live. Swapping the
image on the existing service is what keeps it working.

Full record: `plans/cutover-validation-log.md`. Feature plans live in `plans/`.

### Build identity is stamped, not derived

`GET /version` reports `version`, `commit` and `builtAt` from package variables in
`internal/httpapi`, set by `-ldflags` in both Dockerfiles.

**`-X` takes a fully qualified symbol, and a wrong one is ignored with no
error.** `main.version` resolves only for a var in package `main`; the variables
have to live in the package that reads them. A build that loses the flag still
produces a working image that reports `0.0.0-dev` forever.

**The build context is `backend/go` and `.git` is at the repository root**, so no
VCS metadata exists to read. `COMMIT` comes from `RENDER_GIT_COMMIT`, which Render
sets and translates into a build argument automatically; an explicit `COMMIT` still
wins for local builds. `VERSION` names a release and stays a literal. `BUILT_AT`
defaults to the build time via `$(date -u)` inside the `RUN`, never via an `ARG`
default — that is fixed at parse time and would report the date the Dockerfile was
written.

**Render passes every service environment variable to the build as an argument**,
secrets included. The Dockerfile declares only `VERSION`, `COMMIT` and
`RENDER_GIT_COMMIT`, so an undeclared one is inert and cannot reach the image.
Verified by building with a secret as a build argument and searching the resulting
binary. **Never declare an `ARG` for a credential**, even briefly.

The "Build and smoke-test image" job asserts the stamps reached the binary. That
job runs with `defaults.run.working-directory: backend/go`, so its build context
is `.`, not `backend/go`.

## The instance index

`GET /instances` lists other people's deployments and whether each one is
serving its own images. What an instance has to expose to be listed is in
`docs/docs/instances.mdx`, and these are the rules a change has to respect.

**The list lives in one JSONB row, and read-modify-write needs the lock.**
`repo.UpdateInstances` wraps the read in a transaction holding `FOR UPDATE` on
that row. Drop it and concurrent registrations silently overwrite each other —
verified by removing it: 3 of 8 concurrent writes were lost. The public surface
has no way to notice.

**A registered URL is untrusted input.** The prober dials it. The address block
list lives in `DialContext`, not in handler validation, because a hostname can
resolve differently between validation and use. Every redirect hop is re-checked
too. Response size, timeout and redirect count are all capped.

**Signals are published as components. There is no composite score.** A single
number hides its own weights and the person it is about is the one who would have
to trust it. Image count is excluded on purpose: partial instances are welcome and
counting images penalises exactly what the feature is trying to encourage.

**A failed check never deletes an entry.** A Render free-tier instance sleeps for
hours; a list that empties overnight teaches readers to distrust it.

**Genealogical fields are absent unless the commit resolved.** `upstream` and
`commitAgeDays` are populated only when the reported commit is in the bundled
history. Absent means "we could not tell", which is different from "behind" — a
fork has no shared ancestry to measure against.

**Regenerate the commit bundle after merging**: `cd backend/go && go run
./tools/commits`. The bundle is a snapshot, so an instance running this
repository's newest code reads as "could not tell" until it is refreshed.

**`schema.sql` no longer matches production exactly, on purpose.**
`schema_contract_test.go` fails on any table the EF snapshot does not declare, so
`InstanceIndex` is listed there with its reason — the same convention as
`knownExtras` for routes. **Apply the table to production before deploying code
that reads it:**

```sql
CREATE TABLE IF NOT EXISTS "InstanceIndex" (
    "Id"        integer     NOT NULL PRIMARY KEY,
    "Instances" jsonb       NOT NULL,
    "UpdatedAt" timestamptz NOT NULL,
    CONSTRAINT "CK_InstanceIndex_Singleton" CHECK ("Id" = 1)
);
```

No seed row: reads treat a missing row as an empty index, so applying the table
is the only step.

**A local instance cannot successfully register anything.** The block list refuses
loopback and RFC1918, which is every address reachable on a workstation, so the
happy path is covered by the integration suite with a fake prober and the real
prober is covered by `internal/instances` tests. A green local run proves the
refusals, not the success.

### Never point a local instance at the production database

`background.runJob` runs each cleanup job **once at startup**, before its daily
ticker. `StatsCleanup` is unconditional and issues `DeleteDailyStatsBefore`, so
a second Go instance booting against production deletes rows immediately.

Production is safe from this only because there is a single instance. Use a
local database.

### CORS is not a route concern

`withCORS` mirrors the C# `UseCors("AllowAll")` policy — any origin, any method,
any header. It is invisible to `routecheck` and to a route sweep, because a
route is a path and a method and CORS is a **header**. A route sweep will never
catch a regression here. Header behaviour needs its own tests: `cors_test.go`.

## Storage and images

The bucket is **Tigris**, private, fronted by the Cloudflare Worker in
`scripts/mirror/worker`, which signs SigV4 and caches at the edge. There is no
public read path. Backend code and that worker are what stand between you and a
broken public site.

Worker secrets are **not** in `wrangler.toml`. They are set with
`npx wrangler secret put` and sourced from the gitignored `scripts/mirror/.env`.

### Use `storage.ObjectKeyCandidates`, never construct a key

`Image.Extension` records the format an image *was*, not the one it is *stored*
as. Every PNG in the catalogue is stored as WebP and served back at its `.png`
path. A plain `{id}{ext}` therefore finds nothing for those rows and reports
them absent.

This is the single most repeated trap in the repo's history. It has reached the
upload path, the reconciliation page and the CDN worker at different times.

### Conversion order: flip the column, then delete the original

The free tier is a fixed cap, so writing every new object alongside the old ones
before deleting any of them can exceed it for the entire run rather than
momentarily. `scripts/mirror/webp_backfill.py` batches: convert, upload,
**verify the written size by reading it back**, flip `Extension`, then delete
that batch's originals.

Flipping `Extension` *before* deleting is the load-bearing ordering. A row still
naming the old extension makes the API hand out a URL for an object that is
gone — a 404 handed to a client rather than to the bucket.

Runbook is embedded in the tool: `python3 scripts/mirror/webp_backfill.py --runbook`.

### WebP quality: 85

Measured on this project's own images with SSIM against the original. 85 is the
cheapest quality where nothing sampled fell into visible loss, and it saves more
than the commonly quoted range for JPEG→WebP because anime art is mostly flat
colour. **Do not retune from a blog post** — re-measure the same way if the
corpus changes.

Weigh size over quality deliberately: most consumers are Discord bots rendering
at embed size, not a wallpaper site printing to a 4K monitor.

**Dimensions are never changed.** Re-encoding alters bytes only, so `Width` and
`Height` stay correct and the resolution filter keeps working. Resizing changes
both and is a separate decision.

**AVIF renders in Discord** — confirmed in their engineering blog and verified
here. It is still not worth taking: encoding is far slower than WebP, which
lands on every upload, and content negotiation gains nothing because the main
consumer expresses no preference.

**Animated WebP is the obvious next saving** and the guard has not been lifted.
`TryConvertWebP` returns early on `isAnimated`; changing that also moves
`Extension` while `IsAnimated` stays true, so clients that infer animation from
the extension break. Discord reports large savings converting GIFs, but measure
it here before believing a number taken from their emoji pipeline.

### Both Worker tests are mandatory

```bash
cd scripts/mirror/worker
node webp_candidate.test.mjs   # the extension arithmetic
node handler.test.mjs          # drives the real fetch()
```

**`handler.test.mjs` matters more.** `node --check` parses without executing,
so it passes an unbound identifier that only fails at request time — and a
change once shipped that way took the CDN down for an extended period before
anyone noticed. The unit test for the pure function passed too, because the
defect was in its caller. **Execute the path, don't just parse it.**

### Hash before converting

The perceptual hash is computed from the original upload bytes, never from the
re-encoded copy. `upload.go` converts for storage *after* hashing. Reversing
that makes dedupe unreliable across the format boundary, and almost every
upload now sits on that boundary.

### The duplicate threshold is validated

`PerceptualHash` holds Go hashes throughout; the pre-migration values are
preserved in `PerceptualHashLegacy`. **Do not drop that column** — it is the
only record of what the original corpus contained.

The threshold of `4` was originally chosen against an algorithm that did not
reproduce itself, so it was re-measured against the real distribution and is
now correct. Do not retune it by feel. `internal/images/phash` intentionally
does not chase bit-exactness with C#, because that gap moves with the CPU. See
`backend/go/tools/oracle/README.md`.

## Query parameters are case-insensitive

The C# service bound `[FromQuery]` models, and ASP.NET matches query names
case-insensitively. `url.Values.Get` in Go is an exact map lookup, so every
parameter became case-sensitive on the day the port landed.

Reads go through `queryParam`, which tries the exact spelling then falls back to
a case-insensitive scan; `stringListParam` does the same. **Values are never
lowercased** — `reviewStatus=Accepted` is an enum value.

Two traps:

**Do not "fix" this with a middleware** that lowercases parameter names on the
way in. It looks tidier — one place, no call sites — and it breaks everything,
because the code reads `query.Get("pageSize")` while the key has become
`"pagesize"`. That version was written, caught by its own new test, and thrown
away. Normalising at the point of use is the version that cannot be forgotten.

**Parity sweeps cannot catch this class of bug.** They call both backends with
identical casing, so both agree perfectly — on inputs nobody sends. Sweep with
**mixed casing** to catch a divergence.

## Reconciliation is destructive-adjacent

`/storage-diff` lists objects with no catalogue row and rows with no object, and
renders a delete control beside every orphan. Get the comparison right: an
object is an orphan only when it matches no row's candidate keys, and a row is
missing only when *none* of its keys are in the bucket. Testing each key
independently is wrong in both directions.

A healthy bucket reads **orphans = 1, missing = 0**. During a bulk conversion
orphans climb legitimately — a new-format object whose row has not been flipped
yet is an orphan by definition. A **missing** count above zero is never
expected; stop.

When this page disagrees with reality, believe it over the database until a
probe proves otherwise, and never click delete on a list whose construction you
have not read.

## Verifying your own work

- **A green suite is necessary, not sufficient.** Every significant defect in
  the cutover was a *silent divergence from the C# oracle* — valid, complete,
  wrong answers rather than failures. The port's tests agreed with the port
  because they encoded the port's assumptions, not the contract.
- **Do not report "all clear" without probing.** Twice in one evening the system
  was reported working while it was not. Both were caught by looking, not by
  testing.
- **Execution beats parsing.** `go build`, `go vet` and `node --check` all pass
  an unbound identifier.
- **When a probe fails, suspect the probe first.** A DNS typo and a wrong tuple
  unpack produced confident false alarms; a bad cache-buster parameter wrote junk
  entries into the real edge cache.
- **Reconcile totals from independent sources** — dashboard, live API, object
  store — before declaring a migration complete.
- **Read the code that will run** before telling someone a command works.

## Data and migrations

- `waifu-public.sql`-style dumps are **not** guaranteed current. Reconcile
  against the live API and the local mirror before trusting one as a manifest.
- `Image.PerceptualHash` is a Postgres `bit(64)`, which `pgdump` renders as 64
  binary characters, not hex. The API exposes 16 lowercase hex chars. Mixing
  them up silently breaks comparison.
- The local corpus holds the originals and is the rollback for format
  migrations. Keep it untouched.
- The test database must match production exactly. `schema.sql` is not a
  convenience fixture — where it drifts from the real database, tests pass and
  production breaks. Compare the live table against what the code writes rather
  than against `schema.sql`; rolled-back transactions are the check:

  ```sql
  BEGIN;
  INSERT INTO "Users" ("DiscordId","Name") VALUES ('probe','probe') RETURNING "Id";
  ROLLBACK;
  ```

### Render's connection string is Npgsql format, not a URL

`ConnectionStrings__DefaultConnection` is keyword/value with **semicolon**
separators, because that is what the .NET service reads and what
`scripts/mirror/mkconn.py` prints for the dashboard.

pgx does not accept that. `ParseConfig` understands a `postgres://` URL or
space-separated pairs, so a semicolon-separated string parses to *nothing* and
every field falls back to its default. `internal/config.normalizeConnectionString`
converts it, and handles three things the obvious implementation misses: the
password is percent-encoded by `mkconn.py` and Npgsql decodes it, `SSL Mode`
has a space in the key, and `SSL Mode=Require` has a capital R that pgx rejects.
URLs pass through untouched.

## Known licensing trap

`SixLabors.ImageSharp` **4.x is commercially licensed** and fails Release
publish without a `SixLaborsLicenseKey`. The project is deliberately pinned to
**3.1.12**, which already ships `Formats.Webp.WebpEncoder`. Upgrading buys
nothing and breaks the deploy. 3.1.12 carries open NuGet advisories (NU1902 /
NU1903) — a known, accepted trade-off pending a decision on licensing or
replacement. It applies to the C# oracle only; Go uses libwebp.