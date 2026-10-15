# Plan: Instance index

A public list of other people's hosted instances of this software, a Discord
command to register one, and a periodic check that scores each entry.

Reshaped after discussion on 2026-10-13. The original draft assumed a C#
backend, an always-on bot, and GitOps registration. All three are wrong now —
see [Superseded](#superseded-decisions).

## Status

Step 1 landed 2026-10-15: `GET /version` and working build injection. Step 2
(`GET /instances`) is next.

Two pieces, split because they are worth different amounts:

- **`GET /version`** — a handshake any instance can answer. Useful on its own,
  and worth doing regardless of whether the index gets built. **Done.**
- **The index** — list, registration, checker, page. **Not started.**

### What step 1 changed against the plan

- **The CI assertion could not go where this document said.** The image smoke
  test only proves the service reached the database and exited, so there is no
  server to curl. The stamps are asserted from the startup log instead, which is
  emitted before the database attempt the smoke test already checks. A unit test
  covers handler-to-variables; the image run covers variables-to-binary.
- **`builtAt` needs no CI cooperation**, but not in the way it first reads: an
  `ARG` default is fixed at parse time, so every build would report the date the
  Dockerfile was written. The substitution is `$(date -u ...)` inside the `RUN`.
- **`buildInfo()` no longer takes `version` from `debug.ReadBuildInfo`.** Two
  sources for one field is how `/healthz` and `/version` drift apart. `vcs.*`
  settings are still read; they only exist for a build in a VCS checkout, which
  the image is not.
- **Render is still unverified.** Nothing in the repository records what it
  injects. Until it does, expect `commit` to be `unknown` in production and a
  startup warning saying so — that is the intended reading, not a fault.

## Scope

**In:**

- `GET /version` — public, no database access
- Build-time injection of version and commit into `buildInfo()`
- `GET /instances` — public list
- `GET /instances/admin` — the same plus `discordUserId`, admin-authenticated
- `POST /instances` — admin-authenticated, validates before storing
- A Discord `/register` slash command
- A 24h background prober producing per-instance signals
- A frontend page rendering the list

**Explicitly out:**

- Public/anonymous submission. Registration is admin-gated; the bot is the only
  writer.
- Proof of domain ownership beyond the handshake. No DNS TXT.
- Federation, nodeinfo, ActivityPub, anything peer-to-peer.
- Team instances — one owner Discord ID per instance.
- A badge system. The reward is a Discord role.
- GitOps. Registration is manual and infrequent.
- Availability while Render is down. Explicitly accepted — see
  [Availability](#availability-and-what-it-costs).

## The handshake

`GET /version`, public, and answerable **with the database switched off**.

```json
{
  "software": "waifu-im",
  "version": "7.12.0",
  "commit": "9544404",
  "builtAt": "2026-10-15T09:12:44Z"
}
```

**The governing constraint is that it must never touch `s.repo`.** That is what
makes a CDN-only instance — a Worker with no Render behind it — verifiable. If
the handler pings the database, every minimal instance fails for no interesting
reason, and the "a CDN is fine" case stops working.

- `software` is a constant.
- `version` and `commit` are injected at build time via `-ldflags` and read
  from package variables. `builtAt` is stamped in the Dockerfile and needs no
  CI cooperation.
- No owner field. An instance should not have to know its Discord ID to be
  verified — that is recorded at registration, from the invoker.

`/version` rather than `/.well-known/waifu-im`. The convention buys
cross-project discoverability, and nothing scans for `waifu-im`. With only this
instance's own checker consuming it, a predictable ordinary path is equivalent
and easier to document. Moving later is one line if a third-party crawler ever
appears.

### `version` already means two things here, and this plan picks one

There is an **API contract version** in this repo — `info.version` in
`openapi/WaifuApi.Web.json`, produced by the C# assembly's
`AssemblyInformationalVersionAttribute` (`Program.cs:148`), and published by the
docs as `v{major}` (`docusaurus.config.ts:8`). `Accept-Version` is documented in
`versioning.mdx` and **enforced nowhere**: no handler reads it in Go, C# or the
Worker.

So the two live values are already inconsistent — `HEAD /healthz` reports Go's
own build identity, the docs report the C# contract version, and nothing ties
them together.

**Decision: `/version` reports the software release, not the API contract
version.** The contract version stays owned by the OpenAPI spec and the docs,
because that is where it already lives and nothing needs a second source. The
word "version" means different things in two places, which is a papercut to
document rather than a defect to fix now.

This becomes wrong the moment `Accept-Version` is enforced. If that lands,
`/version` gains an `apiVersion` field from the same constant the middleware
reads, so the two cannot drift.

### A semver here is descriptive, not a signal

A fork can bump `version` to look current. `version` is therefore **not** a
trust input — it appears in the handshake and is not scored, because a string
the instance reports about itself is trivially gameable.

### `builtAt` is what replaces commit age

`builtAt` is stamped at image build time. It answers "how long has this
deployment been standing still", which is true regardless of whose code it is:
an instance nobody has redeployed in three months is running old code no matter
what that code is. It is a genuine staleness signal that survives forks, and it
costs nothing to obtain.

### Version injection is real work, and it is already half-done wrong

`backend/go/Dockerfile:33` passes `-X main.version=${VERSION}`. **That flag is a
no-op**, twice over:

- `cmd/server/main.go` declares no `version` variable. `-X` naming a symbol that
  does not exist is ignored by the linker with no error.
- Nothing sets the `VERSION` build arg. The only `VERSION` build arg in the repo
  is in the commented-out docs service (`docker-compose.preprod.yml:51`).

Hence `buildInfo()` reporting `build keys: ['goVersion', 'startedAt']` while
appearing to have been given a version.

Three changes:

1. Package-level vars in `internal/httpapi` (the handler's own package —
   `-X` takes `importpath.name`, so `main.version` only resolves for a var in
   package `main`).
2. Dockerfile `ARG VERSION` / `ARG COMMIT` / `ARG BUILT_AT`, stamping all three
   into that package, replacing the dead `main.version` flag.
3. `buildInfo()` reads them, so `/healthz` carries them for free. That is the
   part worth having regardless of whether the index is ever built.

**A silent no-op flag is exactly the failure this repo keeps making**, so the
injection gets a test: the CI image smoke test already runs the built binary
(`go-backend.yml`, "Build and smoke-test image"), so assert `/version` returns
something other than the default. A build that lost its `-ldflags` fails the
build instead of deploying an instance that reports `0.0.0-dev` forever.

### Where the values come from, and the one open question

Git history cannot be read at build time. The build context is `backend/go`
(`docker-compose.yml:9`, `go-backend.yml:167`) and `.git` lives at the repository
root, so it is never in the context — that is the structural reason VCS stamping
yields nothing, not a missing flag. Both `VERSION` and `COMMIT` must be passed
in by whoever builds.

- **CI** has the checkout and can pass both as `--build-arg`.
- **Render is unverified.** Nothing in the repository records what it injects.
  Establish that before step 1 is called done; if it cannot supply a commit, the
  field degrades to `"unknown"` and only `builtAt` carries staleness. That is an
  acceptable outcome, and better than a value that looks real and is not.

## The public list

`GET /instances`, unauthenticated:

```json
{
  "instances": [
    {
      "url": "https://alt.example",
      "name": "someone's instance",
      "uptime": 0.994,
      "schema": "ok",
      "images": "ok",
      "builtAgeDays": 12,
      "upstream": null,
      "commitAgeDays": null
    }
  ]
}
```

No `discordUserId`. No composite score. No image count.

The three nulls are the point of [the fork section](#forks-and-what-a-commit-hash-can-answer):
`upstream`, `commitAgeDays` and anything else derived from a commit are absent
unless the instance is running code from this repository, and `null` says "we
could not tell" rather than implying the instance is behind.

**Two endpoints, not one with conditional fields.** With a single endpoint the
leak risk is "did the auth check fire on this path", which is not obvious by
inspection. Two routes means the public response type has no ID field in it at
all and cannot leak. This mirrors the existing split in `reports_users.go`,
where the admin listing is a separate shape.

## Forks, and what a commit hash can answer

**A commit hash is a fingerprint of the instance's code, not of this
repository's.** It can only be looked up in one history. Someone running a fork
— or a clean reimplementation — has a hash that resolves nowhere, and there is
no shared ancestry against which "how far behind" could be measured. Computing
one anyway would produce a number with no meaning behind it.

So the entire genealogical question collapses to a single bit: **does this
commit exist in upstream?** Either it does and the age is free, or it does not
and every derived field is absent.

**The index promises compatibility, not provenance.** A fork that
reimplements the API faithfully can answer `/version`, return the right shape
from `/images`, and serve images that do not 404 — and will therefore pass every
signal while sharing no code with this project. That is the intended outcome,
not a hole in the scoring. What the list is for is "will this work if I point
my client at it", and that question is answerable without knowing who wrote it.

Signals are therefore in two classes, and the split is enforced rather than
advisory:

- **Behavioural** — answerable from the instance alone, populated for everyone.
- **Genealogical** — needs this repository's history, populated only when the
  commit resolves. Always nullable, never defaulted to a number.

## Trust

Objective components only, published individually. **No single number.**

| Component | Class | How it is measured |
| --- | --- | --- |
| `uptime` | behavioural | checks passed ÷ checks, trailing 30 days |
| `schema` | behavioural | does `/images` return the expected response shape |
| `images` | behavioural | does the URL from that response resolve |
| `builtAgeDays` | behavioural | days since the deployment's build timestamp |
| `upstream` | genealogical | does the reported commit exist in this repository |
| `commitAgeDays` | genealogical | only when `upstream` is true |

**`version` is not a signal.** It is a string the instance reports about itself
and a fork can bump it freely. It appears in the handshake and is excluded from
scoring.

**Deliberately excluded:**

- **Image count.** Partial instances are explicitly welcome. Completeness in the
  score penalises exactly what this feature is trying to encourage.
- **Image content comparison.** Verifying that an instance holds our corpus
  means hashing every image on both sides, breaks whenever anyone adds one
  image, and answers a question nobody is asking.
- **Popularity.** Needs votes, has a gaming vector, and turns a technical list
  into a leaderboard. This is how instance lists die.
- **"Goodness".** Drop the word. Unmeasurable terms are arguments waiting to
  happen.

A composite `score: 82` hides its own weights — someone at 82 will ask why, and
the answer is arbitrary. Named components sort just as well and are falsifiable:
an operator can check the uptime claim against their own experience.

A tier badge is acceptable only if **derived from thresholds written down next
to it**. Derived-and-visible is honest; derived-and-opaque is what people
distrust.

## Verification

Probed against the instance's base URL. One request each; nothing sweeps the
corpus.

| Check | Class | What it proves |
| --- | --- | --- |
| `GET /version` | behavioural | runs this software, and which build |
| `GET /images?pageSize=1` | behavioural | shape compatibility, and that it is usable at all |
| `HEAD` the returned image `url` | behavioural | bucket and `Cdn:BaseUrl` wiring agree |
| resolve `commit` upstream | genealogical | upstream or fork — never a number for a fork |

The third row is the one that earns the name trust score. It exercises the
`Image.Extension` ↔ `{id}{ext}` key invariant end to end — the mismatch
described in `AGENTS.md` that produces images which 404.

`GET /ping` is deliberately not counted. It proves only liveness, which
`/version` already covers better.

**Fetching is untrusted input.** Every Discord member who can run `/register`
can make the backend issue an HTTP request. Block `169.254.169.254`, RFC1918,
`127/8` and `::1`; force HTTPS; cap response size, timeout and redirects. The
registration is admin-gated, which lowers the risk to social engineering the
operator rather than the public — the block list is still required.

## Registration

Max runs an instance, finds the Discord server, runs `/register <baseUrl>`.

1. **Prerequisites are his problem.** Docker, a bucket, a backend deployment, a
   Worker or resolvable `Cdn:BaseUrl`, and `scripts/mirror/import_supabase.py`
   for the archive. The command does not walk him through these.
2. **`/register <baseUrl>`** — the bot defers an ephemeral reply, because a
   multi-endpoint probe will exceed Discord's three-second ack window.
3. **Capture** `interaction.user.id`. It is the invoker's Discord identity,
   free from the interaction — no "what is your Discord ID" question, and
   nobody can register somebody else's URL by typing their handle.
4. **Probe** the URL, then store the entry with `discordUserId` and `verifiedAt`
   recorded at registration. The re-check compares against these, not against
   whatever answers later.
5. **Grant the Discord role**, keyed on the stored ID.
6. **He appears on the public list.**

**Key everything on the numeric Discord ID, never the username.** Usernames are
mid-migration to globally-unique and still resolve to a different account after
a rename. Keying the badge grant, trust model and de-duplication on a username
means one rename silently orphans someone's role, or hands it to someone else.
Cache the display name for the admin panel as a label, never as a key.

## Signals and staleness

Signals are stored alongside the score so a drop is explainable:

- `signals: [{ name, ok }]` persisted next to the score
- a failed check marks the entry `Stale`, it never deletes it — a Render
  free-tier instance sleeps for hours and that is not delisting-worthy
- the score is recomputable by anyone from the published signals

## Storage

**A single Postgres row holding a JSONB array.** `get → mutate → set` on one
column, atomic, auditable, no new infrastructure.

The original draft justified this by wanting the index to survive the backend
going down. **That justification has been dropped** — "backend down is not our
problem" is now an explicit, accepted limitation, because running it from
Postgres means the index is down in exactly the moment it would be useful.

Alternatives and why they are worse *given that relaxation*:

- **S3 JSON file.** The Worker will not serve it: `worker.js`'s
  `looksLikeImage` regex requires `.png|.jpg|.gif|.webp`, so `instances.json`
  falls through to `handleApi`, is proxied to `API_ORIGIN` and returns 404.
  Needs a new Worker branch for a signed GET of a non-image object.
- **Local file.** Render's filesystem is ephemeral; the list vanishes on the
  next deploy.
- **GitOps.** The bot opens a PR and CI redeploys. Rejected: registration is
  occasional and manual, and PR-per-instance is friction for no benefit.

## Components

| Piece | Notes |
| --- | --- |
| `/version` handler | package vars set by `-ldflags`; must not reference `s.repo` |
| build injection | `backend/go/Dockerfile`; also feeds `/healthz`; tested so a lost flag fails CI |
| route contract | both endpoints need a `knownExtras` entry with a justification, or `route_contract_test.go` fails; `routecheck` flags them too |
| `GET /instances` | public, no admin fields in the response type |
| `GET /instances/admin` | adds `discordUserId`; gated by `s.requireRole(w, r, config.RoleAdmin, …)` — a method, not a bare function |
| `POST /instances` | admin policy on writes, probes before storing |
| prober | injectable interface so tests can fake it — the integration suite needs `WAIFU_TEST_DATABASE_URL` and cannot make real HTTP calls. Also the only place a commit is resolved, and resolution is allowed to return "not upstream" |
| re-check | `internal/background`, alongside `StatsCleanup`, same daily ticker and scope pattern |
| bot `/register` | `discord.app_commands`; the existing `bot/bot.py` is prefix-command based |

Existing conventions to match: `PaginatedList<T>` + the pagination helper for
list responses; `handleUpdateUserBan` in `reports_users.go` for the admin-gate
pattern; `queryParam` for query reads; `apierror` for typed failures.

## Availability, and what it costs

The public list is served by the backend from Postgres. When Render is down the
list is unavailable, and this is accepted rather than engineered around.

What *is* accepted without qualification:

- **The bot is not 24/7.** It runs when the operator starts it. Registration
  being an interactive slash command does not need a persistent process.
- **Scores go stale.** The prober is a daily background job, so a failure is
  only noticed up to a day later.

## Build order

1. `/version` + working build injection. Useful standalone; the version
   injection is worth having regardless. Confirm what Render can pass before
   calling it done.
2. `GET /instances`, public, from the JSONB row.
3. `/register` — admin POST, validated by hitting `/version`.
4. The 24h prober and the signals it writes.
5. The frontend page, last, because it is the only part needing the frontend.

Step 3 cannot work before step 1: an instance cannot be verified until it can
identify itself.

## Superseded decisions

| Original | Now | Why |
| --- | --- | --- |
| `/.well-known/waifu-im` | `GET /version` | nothing crawls for `waifu-im`; the convention only pays off with an external ecosystem |
| Owner ID in the public index | Admin-only endpoint | badges need the ID, the public does not. Masking (`dunk…kron`) solves nothing — still a stable public identifier |
| Single composite score | Published components | a lone number hides its weights and invites argument |
| Resilience to backend outage | Accepted limitation | running it from Postgres puts the index down exactly when it would help |
| GitOps registration | Direct admin POST | registration is manual and infrequent |
| Bot assumed 24/7 | Runs when started | registration is interactive; no persistence needed |
| Image count in the score | Excluded | would penalise the minimal instances the feature wants |
| `BackgroundService`, `StorageDiffController`, `TestHarness` | `internal/background`, `handleUpdateUserBan`, `WAIFU_TEST_DATABASE_URL` | the backend is Go; the C# names no longer exist |