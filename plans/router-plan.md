# Image Router — Plan

Status: **Brainstorm / spec — not yet implemented.**
Date: 2026-11-09
Owner: (operator)
Related: `docs/docs/instances.mdx`, `scripts/mirror/worker/worker.js`, `backend/go/internal/background/instances.go`

---

## 1. TL;DR

A sibling Cloudflare Worker (`waifu-router`) that does **not** serve image bytes and
does **not** proxy traffic. It answers one question for anonymous clients:

> "Who can serve this image right now, and is it actually there?"

It returns a JSON object containing the **community-served image URL** from a
registered instance that responded `200` to a live probe, plus flags about the
probe, plus a **trusted fallback URL** (our own CDN) if nobody answers.

- Hostname: `https://waifu-router.waifu-api.workers.dev`
- Path: `/router/images?...`
- Storage: KV (snapshot) + Durable Object (metrics) + Cache API (60s responses)
- Metrics page: **Option B** — page in the main frontend (e.g. `/router`), polling
  the Worker's `/stats` JSON endpoint. No external DB.
- Request path: **100% Cloudflare. Zero Render contact.**
- Control plane (snapshot publishing) may still run on Render as a background
  cron — invisible to router users. To be decided (§10).

This is opt-in, best-effort, and explicitly **not fully trusted**. Fast when
cached, slower on cold probes, and the caller accepts that headache in exchange
for a possibly-faster byte fetch from a nearer community instance.

---

## 2. Goals & Non-Goals

### Goals

1. Anonymous clients can discover the fastest-available community instance for a
   given image without us proxying bytes (no egress bill for us, no Render cold start).
2. Router responds in ~5ms when warm (cache hit), ~200–1000ms on cold probe.
3. Community instances get useful traffic **for free**, with their consent implied
   by registration.
4. Metrics: we can see how many people actually used the router, community vs.
   fallback ratio, per-instance wins.
5. Reuse existing public dumps (`/images`, `/instances`) — no new backend DB export.

### Non-Goals

- Proxying or streaming image bytes through the router.
- Serving authenticated/private content (favourites, albums, review queue, reports).
- Guaranteeing the community image is byte-identical to ours (see §7 flags).
- Replacing `waifu-cdn` — our CDN remains the trusted fallback.
- A ranking/score for instances on the public index — router scoring is internal
  and transparent (§6), it does not change the "no score, no ranking" Instances page.

---

## 3. Decisions Locked In (from brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Cache acceptable? | **Yes.** 60s positive, 5m negative, stale-while-revalidate. |
| 2 | Verification strictness | **200 = pass.** Dimension/size/content-type checks are extra **flags**, not gates. |
| 3 | Image format | Only **our** instance has full WebP conversion. Others serve original `.jpg`/`.png`. Router must probe canonical + `.webp`, not assume WebP. |
| 4 | Operator consent | **Registration = consent** to receive HEAD probes. No separate opt-in flag for v1. |
| 5 | Trust label | **`community-served`** (not "untrusted", not "verified"). |
| 6 | Auth strictness | **None.** Separate public route; no private data, no auth forwarded, so `Authorization`/`X-Api-Key` are simply ignored. Rate-limit instead (§9). |
| 7 | Placement | **Pure Cloudflare Worker** for the request path. Not Render. Render egress/bandwidth caps + cold starts are the reason. CF bandwidth is effectively unmetered; router JSON egress is trivial anyway. |
| 8 | Worker topology | **Sibling worker** `waifu-router`, not a path inside `waifu-cdn`. Isolated deploy/kill-switch/cache namespace; never risk image bytes for router experiments. |
| 9 | URL format | `waifu-router.waifu-api.workers.dev/router/images?...` query-style (see §5). |
| 10 | Snapshot source | **Public JSON dumps**, trimmed. `GET /images?pageSize={totalCount}&isNsfw=All` + `GET /instances`. No custom DB export. |
| 11 | Metrics storage | **Durable Object** (SQLite-backed, atomic). KV is *not* used for counters. |
| 12 | Metrics page | **Frontend page (Option B)**, polling Worker `/stats`. |
| 13 | Router slowness | Accepted as caller's problem. Router is best-effort, no SLA, client must implement fallback (see §8). |

---

## 4. Architecture

```
                            ┌─────────────────────────────────────────────┐
                            │  Cloudflare (account: waifu-api)            │
                            └─────────────────────────────────────────────┘
                                      │
  Client (anon) ──GET /router/images?id=8613──▶ waifu-router Worker
                    (5ms warm, ~500ms cold)           │
                                                      ├─ Cache API: 60s response cache (SWR)
                                                      ├─ KV: snapshot {instances, images(trimmed), publishedAt}
                                                      ├─ race HEAD probes → top-5 instances
                                                      └─ DO (RouterMetrics): atomic counter increment
                                                              │
                                                              ▼
                    ◀── JSON {url: community, trusted:false, checks:{...}, fallbackUrl: ours}
                    Client fetches bytes directly from community instance (or fallback)

  Frontend (/router page) ──poll /stats──▶ waifu-router Worker ──▶ DO ──▶ JSON metrics

  Publisher (cron, §10) ──GET CDN /images + GET Render /instances──▶ trim ──▶ PUT KV snapshot
                            (control plane only, never on request path)
```

Why CF: Render is Oregon-only, cold-starts, bandwidth-capped/charged. CF Workers
are edge-local, effectively unmetered bandwidth, native parallel `fetch()` race,
and the probe latency measured from the user's colo (not Oregon) is a better
proxy for "best serving instance" for that user.

Existing infra we already own: `waifu-cdn.waifu-api.workers.dev` (`scripts/mirror/worker/`).
Router is a second worker under the same `*.waifu-api.workers.dev` account subdomain:
`waifu-router.waifu-api.workers.dev`. No new Cloudflare account needed.

Cache API note: like `waifu-cdn`, a `workers.dev` Worker cannot be purge-by-host.
Use a `ROUTER_CACHE_VERSION` bump to orphan stale entries, same trick as
`CACHE_VERSION` in `worker.js`.

---

## 5. URL & API Design

### 5.1 Base

```
https://waifu-router.waifu-api.workers.dev/router/images
```

Trailing `?=` from the brainstorm draft is dropped — real param names only.

### 5.2 Modes (one endpoint, validated)

| Mode | Example | Semantics |
|------|---------|-----------|
| Single (canonical) | `GET /router/images/8613` | Deterministic lookup. Path form, mirrors `GET /images/{id}` on main API. |
| Single (alias) | `GET /router/images?id=8613` | Same as above. Convenience. |
| Batch | `GET /router/images?ids=8613,5497,123` | Return `{items:[...], totalCount}`. Phase 2 candidate. |
| Filtered random | `GET /router/images?isNsfw=false&orientation=portrait&random=true` | Pick one matching image from snapshot, then probe for it. Phase 2 candidate (needs rich snapshot, §6.3). |

Validation rules:

- If `id`/`ids` present → deterministic, ignore filter params (or AND as sanity check).
- If no `id` but filters present + `random=true` → filtered-random.
- If no `id` and no `random=true` → `400`.
- Unknown query params → dropped (after normalization, §5.4), not 400'd.
- `GET` only. `HEAD` may be answered for health, but does not count as a serve (§11).

### 5.3 Query params (mirror main API exactly)

Reuse main API names so clients can swap base URL: `isNsfw` (not `nsfw`),
`isAnimated`, `orientation`, `includedTags`, `excludedTags`, `includedArtists`,
`excludedArtists`, `includedIds`, `excludedIds`, `width`, `height`, `byteSize`.

- Default `isNsfw` = same default as main API (SFW-only). Router must never
  serve NSFW when main API wouldn't for the same query.
- v1: support `id`, `ids`, and pass-through/normalize others. Unsupported filters
  in ID mode are ignored; in random mode, unsupported → `400` with clear message
  rather than silently dropped filtering (dropping `isNsfw` would be a safety bug).
- Case-insensitivity: main API's `queryParam` is case-insensitive (`pageSize` /
  `pagesize` / `PageSize` all work). Router should match that tolerance.

### 5.4 Cache key normalization

Query strings explode cache if not normalized:

- `?id=8613&isNsfw=false` vs `?isNsfw=false&id=8613` = same question, different key.
- Attacker adds `&x=<random>` to bust cache and force probes.

Worker must: parse → whitelist known params → re-serialize in fixed order →
cache under normalized URL. Unknown params never reach cache key or probe logic.

### 5.5 Response shape

Single:

```json
{
  "imageId": 8613,
  "url": "https://api.waifu.best/8613.png",
  "instance": "https://api.waifu.best",
  "extension": ".png",
  "servedBy": "community",
  "trusted": false,
  "warning": "community-served: bytes are fetched directly from a third-party instance, not proxied or byte-verified by this API. Availability and content are the operator's responsibility.",
  "checks": {
    "status200": true,
    "extensionOk": true,
    "contentTypeOk": true,
    "contentType": "image/png",
    "dimensionMatch": "unknown",
    "sizeBytes": 4123111,
    "latencyMs": 138
  },
  "source": "cache",
  "checkedAt": "2026-11-09T14:00:00Z",
  "fallbackUrl": "https://waifu-cdn.waifu-api.workers.dev/8613.webp",
  "fallbackTrusted": true
}
```

Batch/list: `{ "items": [ ...single shape... ], "totalCount": N, "pageNumber": 1 }`
mirroring `GET /images` pagination envelope.

If no community instance answers 200: return fallback with `trusted: true`,
`servedBy: "cdn"`, `url == fallbackUrl`. Never 404 a valid image ID just because
community is down. Only 404 if the ID doesn't exist in our snapshot at all
(`400`/`404` with `error` field consistent with main API).

---

## 6. Snapshot

### 6.1 Source: two public dumps

No custom export. The publisher fetches:

1. **Images:** `GET https://waifu-cdn.waifu-api.workers.dev/images?pageSize={totalCount}&isNsfw=All`
   - Do **not** hardcode `5000`. First fetch `pageSize=1`, read `totalCount`
     (4292 at time of writing, will grow), then fetch `pageSize={totalCount}`,
     or paginate in a loop. `maxPageSize` is currently `-1` (unlimited) but can
     change — pagination loop is the safe assumption.
   - Verified live: 200, 4.3MB, ~3.2s for the full dump via CDN (edge-cached 60s).
2. **Instances:** `GET https://waifu-api-031n.onrender.com/instances`
   - Example (2026-11-08): 5 entries, each `{url, name, uptime, schema, images,
     builtAgeDays, version, lastCheckedAt, ...}`.
   - Filter: `schema == true && images == true`. Keep `url` only for router use.
   - Note: list currently contains both our API origin *and* our CDN as separate
     entries. Router should de-duplicate / treat CDN entry as the byte-serving
     candidate and our API origin as redundant (fallback already covers us).

### 6.2 Trim before KV

Raw dump is 4.3MB (includes `perceptualHash, dominantColor, source, artists{full},
tags{description...}, uploadedAt, albums, favorites`). Worker CPU budget is
~50ms — parsing 4.3MB per request is a non-starter.

Publisher trims to only routing/filtering fields:

```json
{ "id": 8613, "extension": ".png", "isNsfw": false, "isAnimated": false,
  "width": 1000, "height": 1400, "byteSize": 4123111, "tags": ["waifu", ...] }
```

~100 bytes/image → **~430KB** for 4,292 images. Fits KV (25MB/value) easily,
parses in ms, stays in Worker memory.

Final KV value: `{ instances: [...urls], images: [...trimmed], publishedAt, version }`

### 6.3 Why the trimmed dump unlocks filtered-random

`?isNsfw=false&orientation=portrait&random=true` becomes: filter the 430KB
in-memory array (orientation derived from `width>height` etc.), pick random match,
race instances for that ID. Zero DB, zero Render. This is why the query-string
URL format was chosen over a pure path format.

### 6.4 Instance image-URL pattern caveat

`/instances` stores the *API* base URL. Some community instances may serve bytes
from a different CDN host than their API host (their `/images` listing returns
the true `url`, which may not be `{apiBase}/{id}{ext}`).

Router v1 guesses `{instanceBase}/{id}{ext}` + `{instanceBase}/{id}.webp` and
HEAD-checks. Guesses that 404 are skipped; fallback covers the miss. This works
for instances following our Worker pattern (same host serves API + bytes).
Exact-URL lookup via `GET {instance}/images?includedIds={id}` is a v2
improvement if hit-rate is too low.

---

## 7. Probing ("does that specific image return 200?")

### 7.1 Candidate generation

Per instance, per image:

1. Canonical extension from snapshot (`extension` field).
2. `.webp` if different from canonical (covers instances that *did* convert).

Max 2 candidates/instance, max 5 instances → max 10 fetches per cold request.
Well under Workers' ~1000 subrequest limit.

**WebP reality:** only our instance has full WebP conversion for storage savings.
Everyone else is original `.jpg`/`.png` — often 3–10× larger. HEAD-200-fast does
*not* mean download-fast. That's why `sizeBytes` is a flag (§5.5) and docs say
"faster *maybe*". Client chooses.

### 7.2 Race policy

- **Parallel race, first 200 wins.** Not wait-for-all-and-pick-lowest-latency —
  that makes the router itself slow.
- Timeout: **1s** per probe (AbortController). Slow-but-has-it instances get skipped.
- Fan-out: **top 5** healthy instances only.

### 7.3 Probe method

1. `HEAD {candidateUrl}`
2. If `405`/`501` (some Workers/CDNs don't support HEAD), fallback to
   `GET Range: bytes=0-0`. Never download the whole image to check existence.
3. Gate (pass/fail): final status `200` (follow ≤1 redirect) **and**
   `Content-Type` starts with `image/` **and** final URL (post-redirect, query
   stripped) ends with an image extension (`.webp/.png/.jpg/.jpeg/.gif`) **and**
   final host == registered instance host (no redirect to arbitrary domain).
4. Flags (not gates): `dimensionMatch` (`unknown` in v1 — HEAD gives us
   `Content-Length` but not pixel dimensions without fetching/parsing bytes;
   don't fake it), `sizeBytes` (from `Content-Length`), `latencyMs`.

Extension check alone is insufficient — a `.webp` URL can serve `text/html`.
Content-Type check is the real guard; extension is defense-in-depth.

### 7.4 ID collision

Numeric IDs could diverge across partial/fork instances — instance X's `8613`
might not be our `8613`. v1 accepts this risk: 200 + extension + content-type +
host is the gate, dimension/size mismatch is surfaced as flags, and the
`community-served` warning covers it. Byte/perceptual-hash verification is
explicitly out of scope for v1.

### 7.5 Caching

- Cache API, keyed on normalized router URL (§5.4).
- **60s positive, 5m negative** (404).
- **Stale-while-revalidate:** if entry is 61s old, return stale winner instantly
  (~5ms) and refresh in background (`ctx.waitUntil`). Only block on live probing
  with no cached entry at all.
- This is what makes router viable: warm popular images are always instant;
  long-tail pays the probe cost once, then warms.

---

## 8. Client Contract & Docs Wording

Router is **best-effort, no SLA**. Docs must say, in the user's words:

> Using the router is faster *maybe*, trusted *no*, fallback *always available*.
> The URL is community-served by a third-party instance, not proxied or
> byte-verified by this API. It may be larger (png/jpg vs webp), slower, or
> unavailable. On error/timeout (2–3s), fall back to `fallbackUrl`.

Client guidance:

1. Fetch router JSON (cache 60s client-side too).
2. Fetch `url` directly (`<img src>` — no CORS needed; `fetch()` needs
   `Access-Control-Allow-Origin: *` on the community instance, not guaranteed).
3. On failure/timeout → fetch `fallbackUrl` (our CDN, `trusted: true`).
4. Be aware client IP/UA goes straight to the community instance (privacy).

Human cost framing from brainstorm: *"a slow image might take 1–1.5s; if we return
a good instance we might take some ms to return it and its loading might be
faster — anyway, this will be the headache of people who think they will rely
on router."* Docs should own that.

---

## 9. Abuse & Security

| Threat | Mitigation |
|--------|------------|
| Amplification: 1 client req → 10 outbound probes | Cloudflare rate limit on `/router/*`, fan-out cap (5), 1s timeout, negative caching 5m, response cache 60s |
| Open proxy / SSRF | Input is integer ID(s) only. Never accept a target URL/host from the client. Only probe allowlisted instance hosts. Validate final host == instance host. |
| Cache busting via query randomization | Normalized, whitelisted cache keys (§5.4). |
| Auth token leakage | Router never forwards `Authorization`/`X-Api-Key` anywhere; never echoes them; accepts them only to ignore them. |
| NSFW leak via router | Default `isNsfw` mirrors main API; filtering happens in Worker against snapshot *before* probing. Unsupported filter in random mode → `400`, never silently dropped. |
| Malicious instance returning evil URL | Never trust instance-returned `url` verbatim. Reconstruct `{base}/{id}{ext}` and verify status/content-type/extension/host (§7.3). |
| Privacy | No IP/UA stored in metrics (§11). Clients are warned their IP goes to community instance. |

---

## 10. Control Plane: Snapshot Publishing

Router request path never touches Render. *Something* still has to write the KV
snapshot. Options:

| Option | How | Tradeoff |
|--------|-----|----------|
| **A. Render cron → KV REST API (recommended v1)** | Existing daily probe job extended: after probe pass, fetch+trim dumps, `PUT` to KV. Or a small hourly job. | Least new code, reuses probe/health logic. Control plane on Render, data plane pure CF. If Render dies, router serves last snapshot until TTL expiry — graceful. |
| B. CF Cron Trigger Worker | Hourly Worker fetches public dumps, trims, writes KV itself. | Fully off Render, but duplicates probe/health logic in JS. |
| C. Deploy-time script | Generate snapshot during deploy from mirror. | Simplest, but stale between deploys. |

**Recommendation: A for v1**, B as a later purity move if desired. Clarify with
operator: "not on Render" = user-facing path; a background publisher on Render
that runs hourly and is invisible to users is acceptable unless operator wants
zero Render dependency at all.

Publisher cadence: hourly is enough (snapshot drift is slow; daily probe already
exists for health). If hourly Render cron is annoying (free-tier sleep), daily
post-probe publish + on-demand manual publish is the fallback.

KV write: `PUT /kv` via Cloudflare API token (secret, never in `wrangler.toml`,
same discipline as S3 secrets in `worker.js`). Eventual consistency ~60s
matches our 60s TTL perfectly.

---

## 11. Metrics & Visuals

### 11.1 Durable Object — what it is

- **Worker** = stateless cashier. Handles a request, forgets it. Global variables
  may survive briefly in an isolate but Cloudflare can evict/replace isolates
  at any time, and each edge location has separate memory. In-memory counters
  in a plain Worker are unreliable and fragmented.
- **Durable Object (DO)** = stateful cash register with a locked drawer. One
  running instance per ID, single-threaded (atomic increments, no races),
  persistent SQLite-style storage on SSD. Idle DOs hibernate (evicted from RAM,
  disk retained); next request rehydrates. RAM ~128MB/isolate while active;
  storage is separate and persistent. You can have millions of DO IDs on disk;
  only hot ones are in RAM.

One global DO (`idFromName("global")`) is enough for v1. If throughput ever
bottlenecks (single-threaded), shard by day (`metrics-2026-11-09`) or by ID hash.

### 11.2 Why not KV for counters / why not "batch 100 then flush"?

- **KV counters:** read-modify-write races lose increments under concurrency.
  KV is for the snapshot (read-heavy, write-rare), never for counters.
- **Batch-100-then-flush (brainstorm idea):** classic write-behind, but in a
  plain Worker it loses data on eviction (hits 0–99 vanish), fragments across
  edge locations, and has reset races. It only makes sense flushing to an
  *external* DB (D1/Postgres) via a DO alarm.
- **DO storage already *is* the DB:** local SSD, per-hit SQLite transaction is
  cheap and durable. No flush cycle, no loss window. If long-term D1/Postgres
  history is wanted later, add a DO alarm that copies daily buckets out once
  a day. Not needed for v1.

### 11.3 What to count

Increment only on successful `200` JSON serves (not `400`/`404`, not inbound `HEAD`):

- `totalRequests`
- `communityServed` vs `fallbackServed` (headline ratio)
- `cacheHits / cacheMisses`
- Per-instance wins: `{ "https://api.waifu.best": 142, ... }`
- Top image IDs by request count
- Router latency + probe latency buckets, timeout count
- Daily buckets (date → aggregates), 30–90 day retention, pruned

**Privacy:** aggregate counts only. No IP, no UA, no raw query strings stored.

### 11.4 *Not* using Analytics Engine / D1 for v1

- **Analytics Engine:** built for event counting, but sampled, delayed, and
  needs an API token hidden in the Worker to query for a public page. Overkill.
- **D1:** `INSERT` per request just to `COUNT(*)` later = write amplification.
  DO SQLite does this locally with atomicity.
- Cloudflare's built-in Worker analytics (dashboard) already shows raw
  requests/errors, but can't distinguish community vs. fallback or per-instance
  wins — that's the custom DO metrics.

### 11.5 Visual page (Decision: Frontend, Option B)

- New frontend route, e.g. `/router` — separate page from Instances ("who is
  healthy?" vs. "who served traffic?").
- Page polls `GET https://waifu-router.waifu-api.workers.dev/stats` (CORS enabled
  on that endpoint) → Worker → DO → JSON.
- Widgets: big number cards (total served, community %, fallback %), 30d
  sparkline/trend, per-instance table (wins, share), top images, snapshot age /
  healthy-instance count.
- `wrangler` analytics remain the operator view; this page is the public one.

Endpoint shape (draft):

```json
{
  "totalRequests": 12345,
  "communityServed": 9100,
  "fallbackServed": 3245,
  "cacheHits": 8000, "cacheMisses": 4345,
  "perInstance": [{ "url": "...", "served": 9100 }],
  "topImages": [{ "id": 8613, "count": 412 }],
  "daily": [{ "date": "2026-11-09", "total": 500, "community": 380, "fallback": 120 }],
  "snapshotPublishedAt": "...",
  "healthyInstances": 4
}
```

---

## 12. Implementation Phases

**Phase 1 — MVP (ID-only, pure CF, no metrics page yet)**
- [ ] Sibling worker `waifu-router` (`wrangler.toml`, `worker-router.js`) under
      `waifu-api.workers.dev`, deployed independently of `waifu-cdn`.
- [ ] KV namespace + binding for snapshot; Cache API for responses
      (`ROUTER_CACHE_VERSION`).
- [ ] Publisher (Render cron or script): fetch CDN `/images` + Render `/instances`,
      trim, `PUT` KV.
- [ ] `GET /router/images/{id}` + `?id=` alias. Race HEAD top-5, 1s timeout,
      first-200-wins, gate on status/content-type/extension/host.
- [ ] Flags in response, `fallbackUrl` always present, `community-served` warning.
- [ ] Rate limit on `/router/*`, normalized cache keys, CORS on JSON.
- [ ] Docs: router page/section with client fallback guidance (§8).

**Phase 2 — Metrics + filtered selection**
- [ ] DO `RouterMetrics`: atomic increments, daily buckets, `/stats` JSON.
- [ ] Frontend `/router` page (Option B) polling `/stats`.
- [ ] `?ids=` batch support.
- [ ] `?random=true` + filter params (`isNsfw`, `orientation`, tags…) against
      trimmed snapshot.

**Phase 3 — Refinements (only if data justifies)**
- [ ] Exact-URL lookup via community `/images?includedIds=` instead of guessed
      `{base}/{id}{ext}` if hit-rate is low.
- [ ] Dimension verification via Range fetch + image header parse (promote
      `dimensionMatch` from `unknown`).
- [ ] Publisher moved to CF Cron Trigger (zero Render dependency).
- [ ] DO scrapping/sharding if single-threaded throughput bottlenecks.

---

## 13. Open Questions

1. Publisher on Render (hourly/daily cron) acceptable, or must the *control
   plane* also be 100% off Render (CF Cron Trigger) from day one?
2. Snapshot cadence: hourly vs. daily-post-probe vs. manual?
3. Phase 1 scope: ID-only, or ship `?ids=` batch immediately?
4. Frontend `/router` page: build in Phase 2 with DO, or stub `/stats` JSON only
   until then?
5. Docs location: main docs site (`docs/`) vs. a section on the Instances page?
   (Lean: main docs, linked from Instances page.)
6. De-duplication of self-instances in snapshot: exclude `waifu-api-031n...`
   (Render API, no direct bytes) from probe candidates, keep `waifu-cdn...` only?
   (Lean: yes.)

---

## 14. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Router cold probe slower than direct CDN | Users perceive router as slower | SWR cache (warm = 5ms), docs set expectation, client fallback |
| Community instance serves different bytes for same ID | Wrong image shown | Flags + warning; dimension/size checks in v2; fallback always available |
| Community instance hammered because it's fast | Small operator overloaded | Top-5 cap, 60s cache, honor 429 as back-off, per-instance win counts visible |
| Cloudflare subrequest/CPU limits on cold miss | 500s on unusual queries | Max 10 fetches, 1s timeout, trimmed snapshot, normalized keys |
| KV snapshot stale / publisher dead | Router routes to dead instances | `snapshotPublishedAt` surfaced in `/stats`; fallback covers misses; negative cache short |
| `pageSize=5000` cap changes on main API | Publisher dump truncated | Paginate from `totalCount`, never hardcode |

---

## 15. Alternatives Considered & Rejected

| Alternative | Why rejected |
|-------------|--------------|
| Proxy bytes through router Worker | We pay egress, add double-hop latency, become bottleneck — defeats the point. |
| HTTP 307 redirect to instance | Bots/clients cache redirects forever; pinned to dead instances. JSON discovery + client fallback is more robust. |
| Router in Go backend on Render | Cold starts + Oregon-only + bandwidth caps. The thing we're trying to avoid. |
| Router as path in `waifu-cdn` worker | One bad deploy breaks both image bytes and router. Sibling worker isolates risk for ~zero cost. |
| KV for metrics counters | Race conditions lose counts. DO is atomic by construction. |
| Batch-100-in-memory then flush to DB | Data loss on isolate eviction, fragmentation across edges, reset races. DO SQLite per-hit is the safe version of the same idea. |
| Smart JSON from main `/images` (rewrite `url` field) | Breaks "image URL is stable + belongs to the host you queried"; two APIs return different bytes for same ID. Separate router endpoint keeps contracts clean. |

---

*End of plan. Next step when ready: Phase 1 worker + KV + publisher. No code in this doc — spec only.*
