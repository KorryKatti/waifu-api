# Plan: Instance index

A public list of other people's hosted instances of this software, a Discord
command to register one, and a periodic check that scores each entry.

Reshaped after discussion on 2026-10-13. The original draft assumed a C#
backend, an always-on bot, and GitOps registration. All three are wrong now —
see [Superseded](#superseded-decisions).

## Status

Design settled. Not started.

Two pieces, split because they are worth different amounts:

- **`GET /version`** — a handshake any instance can answer. Useful on its own,
  and worth doing regardless of whether the index gets built.
- **The index** — list, registration, checker, page.

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
{ "software": "waifu-im", "version": "1.4.0", "commit": "a1b2c3d" }
```

**The governing constraint is that it must never touch `s.repo`.** That is what
makes a CDN-only instance — a Worker with no Render behind it — verifiable. If
the handler pings the database, every minimal instance fails for no interesting
reason, and the "a CDN is fine" case stops working.

- `software` is a constant.
- `version` and `commit` are injected at build time via `-ldflags` and read
  from package variables.
- No owner field. An instance should not have to know its Discord ID to be
  verified — that is recorded at registration, from the invoker.

`/version` rather than `/.well-known/waifu-im`. The convention buys
cross-project discoverability, and nothing scans for `waifu-im`. With only this
instance's own checker consuming it, a predictable ordinary path is equivalent
and easier to document. Moving later is one line if a third-party crawler ever
appears.

### Version injection is real work

`buildInfo()` currently returns only `goVersion` and `startedAt` in production:

```
build keys: ['goVersion', 'startedAt']
```

Render builds without VCS stamping, so `debug.ReadBuildInfo()` yields nothing
usable and the `version` field is silently absent. The injection belongs in
`backend/go/Dockerfile` via `-ldflags`, and it flows into `/healthz` through
the existing `buildInfo()` for free. This is worth doing on its own merit.

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
      "commitAgeDays": 12
    }
  ]
}
```

No `discordUserId`. No composite score. No image count.

**Two endpoints, not one with conditional fields.** With a single endpoint the
leak risk is "did the auth check fire on this path", which is not obvious by
inspection. Two routes means the public response type has no ID field in it at
all and cannot leak. This mirrors the existing split in `reports_users.go`,
where the admin listing is a separate shape.

## Trust

Objective components only, published individually. **No single number.**

| Component | How it is measured |
| --- | --- |
| `uptime` | checks passed ÷ checks, trailing 30 days |
| `schema` | does `/images` return the expected response shape |
| `commitAgeDays` | commit age against upstream |

**Deliberately excluded:**

- **Image count.** Partial instances are explicitly welcome. Completeness in the
  score penalises exactly what this feature is trying to encourage.
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

Probed against the instance's base URL:

| Check | What it proves |
| --- | --- |
| `GET /version` | runs this software, and which build |
| `GET /images?pageSize=1` | shape compatibility, and that it is usable at all |
| `HEAD` the returned image `url` | bucket and `Cdn:BaseUrl` wiring agree |

The last row is the one that earns the name trust score. It exercises the
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
| build injection | `backend/go/Dockerfile`; also feeds `/healthz` |
| `GET /instances` | public, no admin fields in the response type |
| `GET /instances/admin` | adds `discordUserId`; `requireRole(config.RoleAdmin, …)` |
| `POST /instances` | admin policy on writes, probes before storing |
| prober | injectable interface so tests can fake it — the integration suite needs `WAIFU_TEST_DATABASE_URL` and cannot make real HTTP calls |
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

1. `/version` + build-time version injection. Useful standalone; the version
   injection is worth having regardless.
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