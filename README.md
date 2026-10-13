<div align="center">
  <a href="https://waifu-api-eosin.vercel.app">
    <img src="https://waifu-cdn.waifu-api.workers.dev/8613.jpg" alt="Waifu.im Logo" width="200" style="border-radius: 20px; box-shadow: 0 8px 30px rgba(0,0,0,0.12);">
  </a>

  <h1>Waifu.im API</h1>

  <p>
    <strong>The versatile waifu image provider.</strong><br>
    4,287 anime-style images with filtering, albums, favourites and moderation.
  </p>

  <p>
    <a href="https://waifu-api-eosin.vercel.app"><strong>Website</strong></a> •
    <a href="https://waifu-api-docs.vercel.app"><strong>Documentation</strong></a> •
    <a href="https://waifu-api-docs.vercel.app/playground"><strong>Playground</strong></a> •
    <a href="https://discord.gg/qceZ92p2xK"><strong>Support</strong></a>
  </p>

  <p>
    <a href="https://stats.uptimerobot.com/AbgWCVWsKT"><img src="https://img.shields.io/badge/instance-live-22c55e?style=flat-square" alt="Instance status"></a>
    <img src="https://img.shields.io/github/license/KorryKatti/waifu-api?style=flat-square&color=5865F2" alt="License">
    <img src="https://img.shields.io/badge/Docker-Ready-blue?style=flat-square&logo=docker&logoColor=white" alt="Docker">
  </p>
</div>

> **Community-run fork.** The original project by
> [Buco7854](https://github.com/Waifu-im/waifu-api) is being migrated off its
> original infrastructure. The API contract is unchanged. Endpoints moved, and
> some features are unavailable mid-migration.

## 🚧 Instances

| | |
|---|---|
| API | `https://waifu-api-031n.onrender.com` |
| CDN | `https://waifu-cdn.waifu-api.workers.dev` |
| Website | `https://waifu-api-eosin.vercel.app` |
| Documentation | `https://waifu-api-docs.vercel.app` |
| Playground | `https://waifu-api-docs.vercel.app/playground` |
| Uploads | Open. Discord login required; images enter a review queue |
| Users | Discord OAuth works. Existing accounts did not carry over |

## ⚡ Choosing a host

The two hosts are not interchangeable.

| Request | Host |
|---|---|
| Image bytes, any read | CDN |
| Anonymous API read | CDN (edge-cached) |
| `Authorization: Bearer` | Either. CDN forwards it |
| `X-Api-Key` | **API only** |

Worker header handling — the entire list it forwards upstream:

```
Authorization, Accept, Accept-Language
```

Anything else is dropped. An API-key client pointed at the CDN is
unauthenticated and receives `401`.

Anonymous reads are cached at the edge for 60s. Authenticated requests are never
stored, and `/users`, `/albums`, `/review`, `/auth`, `/reports`, `/stats/admin`
are never cached regardless of credentials. The CDN sets
`access-control-allow-origin: *` on its own responses, so anonymous reads are
reachable from a browser.

### CDN latency

| | TTFB | Total | Cause |
|---|---|---|---|
| Warm | — | ~0.5s | Edge cache hit |
| Cold | 0.7–1.6s | 2–4s | Streams from object storage |

Each Cloudflare data centre caches independently, so any image's second load is
fast. There is no tiered cache and concurrent misses are not collapsed: a
first-time visitor browsing uncached images pays a full fetch per image.

## ✨ Features

- **Archive** — 4,287 images
- **Tags** — character, style and theme filters
- **Albums** — user collections
- **Duplicate detection** — perceptual hashing on upload
- **Filters** — orientation, resolution, file size, NSFW, GIFs, artists, explicit include/exclude
- **Accounts** — Discord OAuth for favourites and albums
- **Moderation** — reports and a review queue
- **Discord bot** — working sample in [`bot/`](bot/)
- **Statistics** — usage and popular tags
- **Sorting** — date, popularity, random

## 🚀 Getting started

```bash
git clone https://github.com/KorryKatti/waifu-api.git
cd waifu-api
cp .env.example .env          # Discord + storage credentials
docker-compose up -d
```

Compose builds the Go backend, the frontend, and Postgres with pgvector. Ports
come from `.env`: backend `8085`, frontend `8084`, database `5432`. Discord
redirect URI is `https://<frontend>/auth/callback`.

Native development:

```bash
docker-compose -f docker-compose.dev.yml up -d   # Postgres + Adminer

cd backend/go  && go run ./cmd/server            # API :8080
cd frontend    && npm install && npm run dev      # UI  :5173
```

## 🏗️ Backends

| Path | Role |
|---|---|
| `backend/go` | The service. Built by compose, runs in production |
| `backend/WaifuApi.*` | C# original. Behavioural oracle — not deployed, not maintained |

The Go suite compares against the C# implementation.
`.github/workflows/docker-image.yml` builds the C# tree to keep that comparison
possible; leave it pointing there.

### Go

Tests require a database and refuse to run without one rather than skipping.

```bash
docker run -d --name waifu-testdb -p 55432:5432 \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=waifutest pgvector/pgvector:pg18
docker exec -i waifu-testdb psql -U postgres -d waifutest \
  < backend/go/internal/store/schema.sql

cd backend/go
WAIFU_TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/waifutest' go test ./...
```

### C#

`SixLabors.ImageSharp` 4.x requires a commercial licence key, enforced in
Release only. Pinned to 3.1.12, which already ships `WebpEncoder`. Render
publishes in Release:

```bash
cd backend
docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet publish WaifuApi.Web/WaifuApi.Web.csproj -c Release -o /app/publish \
  /p:UseAppHost=false /p:Version=0.0.0-local /p:OpenApiGenerateDocumentsOnBuild=false
```

## ☁️ Hosting

| Concern | Host |
|---|---|
| API | Render — JSON, Postgres |
| Images | Cloudflare Worker — SigV4 to a private bucket, edge-cached |
| Anonymous reads | Cloudflare Worker — proxies to Render, 60s cache |

Storage is private. The Worker exists to sign reads from it. AWS S3 or MinIO can
be made public directly and need no Worker.

**Bucket:** Tigris — `https://t3.storage.dev`, region `auto`, bucket `waifu-api`.
Credentials are Worker secrets, never in `wrangler.toml`:

```bash
cd scripts/mirror/worker
grep '^S3_ACCESS_KEY_ID='     ../.env | cut -d= -f2- | npx wrangler secret put S3_ACCESS_KEY_ID
grep '^S3_SECRET_ACCESS_KEY=' ../.env | cut -d= -f2- | npx wrangler secret put S3_SECRET_ACCESS_KEY
npx wrangler deploy
```

### WebP

`Images.Extension` records `.png`; the object is `{id}.webp`. 1,162 rows are
affected. Code that builds a storage key itself misses all of them — use
`storage.ObjectKeyCandidates`.

| Mechanism | Control |
|---|---|
| Legacy archive, converted offline with `cwebp` | `WEBP_FALLBACK` in `wrangler.toml` |
| New uploads, converted at ingest | `Image__WebpConversionEnabled`, `Image__WebpQuality`, `Image__WebpSourceExtensions` |

All off by default. Animated images are always skipped; conversion failure
stores the original bytes rather than failing the upload. Duplicate-detection
hashes are computed from original bytes, before any re-encode.

### Cache invalidation

No purge reaches a `workers.dev` Worker. Bump `CACHE_VERSION` in
[`scripts/mirror/worker/worker.js`](scripts/mirror/worker/worker.js) and redeploy.
Entries are namespaced by it.

## 👑 Admin access

```sql
UPDATE "Users" SET "Role" = 3 WHERE "DiscordId" = 'YOUR_DISCORD_ID';
```

`1` TrustedUser · `2` Moderator · `3` Admin. Production runs this through
Adminer or the Supabase dashboard.

## 📚 Reference

- [`AGENTS.md`](AGENTS.md) — deployment fields, connection-string traps, CORS, duplicate threshold
- [`scripts/mirror/README.md`](scripts/mirror/README.md) — database import, archive mirroring
- [`bot/README.md`](bot/README.md) — sample Discord bot

## License

[MPL-2.0](LICENSE) — file-level copyleft. Forking and self-hosting welcome.

Images are third-party artist work, credited per record in `Images.Source`.

<div align="center">
  <sub>Built with ❤️ by the Waifu.im team and its community fork maintainers.</sub>
</div>
