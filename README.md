<div align="center">
  <a href="https://waifu-api-eosin.vercel.app">
    <img src="https://waifu-cdn.waifu-api.workers.dev/8613.jpg" alt="Waifu.im Logo" width="200" style="border-radius: 20px; box-shadow: 0 8px 30px rgba(0,0,0,0.12);">
  </a>

  <h1>Waifu.im API</h1>

  <p>
    <strong>The versatile waifu image provider.</strong><br>
    Access a curated archive of over 4,200 anime-style images with powerful filtering.
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

> **This is a community-run fork.** The original project by
> [Buco7854](https://github.com/Waifu-im/waifu-api) is being migrated off its
> original infrastructure. The API contract is unchanged, so existing
> integrations keep working — but the endpoints moved, and some features are
> unavailable while the migration is in progress.

## 🚧 Instance status

| | |
|---|---|
| API | `https://waifu-api-031n.onrender.com` |
| CDN | `https://waifu-cdn.waifu-api.workers.dev` |
| Website | `https://waifu-api-eosin.vercel.app` |
| Documentation | `https://waifu-api-docs.vercel.app` |
| Playground | `https://waifu-api-docs.vercel.app/playground` |
| Uploads | Open — requires a Discord login; new images go to a review queue |
| Users | Discord OAuth works; existing accounts did not carry over |

## ⚡ Which host should you use?

Pick this deliberately — the two hosts are not interchangeable.

| What you're doing | Point it at |
|---|---|
| Fetching images (`GET /images`, `/images/sfw`, …) | **CDN** — `https://waifu-cdn.waifu-api.workers.dev` |
| Anything authenticated with an API key | **API** — `https://waifu-api-031n.onrender.com` |
| Anything authenticated with a Bearer token | Either; the Worker forwards `Authorization` |

**The Worker strips the `X-Api-Key` header.** It only forwards `Authorization`,
`Accept` and `Accept-Language`. An API-key client pointed at the Worker is
silently unauthenticated and gets a `401`. The API host is the only one that
passes every credential through.

For anonymous reads the Worker is the faster host: it proxies to the API host
and caches for 60 seconds, so a page load's `/images` + `/tags` + `/stats` is one
edge read instead of three database round trips. The API host is responsive in
its own right — the caching is the advantage, not a workaround for a sleeping
backend. Anonymous reads are browser-reachable too, because the Worker sets
`access-control-allow-origin: *` on its own responses.

### Expect a slow first image

Images are served through the Worker, which caches per data centre.

- **Warm** (~0.5s): cached at the edge, near-instant, repeat visits.
- **Cold** (~2–4s): the first fetch of an image from a given region streams
  straight through from object storage.

The cache is global in the sense that every Cloudflare data centre caches
independently, so the second load of any image is fast — but there is no tiered
cache and concurrent misses are not collapsed, so a first-time visitor browsing
uncached images pays the full fetch each time. Warm it up and it settles.

## ✨ Features

- 🖼️ **Extensive Archive**: 4,287 images and counting.
- 🏷️ **Tag-Based Search**: Filter by character tags, styles, or themes.
- 📁 **Albums**: Organize and share collections of images.
- 👯 **Duplicate Detection**: Perceptual hashing catches re-uploads automatically.
- 🔍 **Advanced Filtering**: orientation, resolution, file size, NSFW, GIFs,
  artists, and explicit include/exclude by file.
- 👤 **User Accounts**: Discord-based auth for favourites and albums.
- 🛡️ **Moderation Tools**: Report system and review queues.
- 🤖 **Discord Bot**: A working sample bot in [`bot/`](bot/).
- 📊 **Statistics**: Usage and popular tags.
- 🎲 **Flexible Sorting**: By date, popularity, or random.

## 🚀 Getting started

```bash
git clone https://github.com/KorryKatti/waifu-api.git
cd waifu-api
cp .env.example .env      # fill in Discord + storage credentials
docker-compose up -d
```

That runs the Go backend, the frontend, and Postgres with pgvector. The
frontend builds against `VITE_API_URL`, and Discord needs its redirect URI
registered as `https://<your-frontend>/auth/callback`.

For active development, run the database in Docker and the services natively:

```bash
docker-compose -f docker-compose.dev.yml up -d   # Postgres + Adminer

cd backend/go && go run ./cmd/server               # API on :8080
cd frontend && npm install && npm run dev         # UI on :5173
```

## 🏗️ Backends

There are two, and only one of them is the product.

| | |
|---|---|
| **`backend/go`** | The service. This is what `docker-compose.yml` builds and what production runs. |
| **`backend/WaifuApi.*`** | The C# original, kept as a **behavioural oracle**. The Go test suite compares against it. Not deployed, not maintained. |

If you are self-hosting, use Go. The C# tree exists so the port can be checked
against the thing it replaced, and `.github/workflows/docker-image.yml` keeps
building it for that reason. Please don't repoint that workflow.

### Go

Tests need a database — they refuse to run without one rather than silently
skipping, because a skipped suite reports green while the service cannot start:

```bash
docker run -d --name waifu-testdb -p 55432:5432 \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=waifutest pgvector/pgvector:pg18
docker exec -i waifu-testdb psql -U postgres -d waifutest < backend/go/internal/store/schema.sql
cd backend/go
WAIFU_TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/waifutest' go test ./...
```

### C#

**Build in Release, not just Debug — Render publishes in Release**, and
`SixLabors.ImageSharp` 4.x requires a commercial licence key that is only
enforced there. The project is deliberately pinned to 3.1.12, which already
ships `WebpEncoder`. Upgrading buys nothing and breaks the deploy.

```bash
cd backend
docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet publish WaifuApi.Web/WaifuApi.Web.csproj -c Release -o /app/publish \
  /p:UseAppHost=false /p:Version=0.0.0-local /p:OpenApiGenerateDocumentsOnBuild=false
```

## ☁️ Hosting notes

The reference deployment does not serve API and images from one host:

| | |
|---|---|
| API | Render — JSON metadata, Postgres queries |
| Images | Cloudflare Worker — signs SigV4 to a private bucket, caches at the edge |
| Anonymous API reads | Cloudflare Worker — proxies to Render, caches for 60s |

The Worker exists because storage is **private**; there is no public read path.
IDrive (among others) no longer permits public buckets. AWS S3 or MinIO can be
made public directly and don't need the Worker at all.

Storage is **Tigris** (`https://t3.storage.dev`, region `auto`, bucket
`waifu-api`). It replaced IDrive, whose free tier caps egress at 1:1 with stored
data — a public image site exhausts that within a month and then fails every
cache miss. Tigris includes free egress. Credentials are Worker secrets, never in
`wrangler.toml`:

```bash
cd scripts/mirror/worker
grep '^S3_ACCESS_KEY_ID='     ../.env | cut -d= -f2- | npx wrangler secret put S3_ACCESS_KEY_ID
grep '^S3_SECRET_ACCESS_KEY=' ../.env | cut -d= -f2- | npx wrangler secret put S3_SECRET_ACCESS_KEY
npx wrangler deploy
```

### WebP

Every PNG is converted and served back at its `.png` path, so **the database
still says `.png` while the object is `{id}.webp`**. There are 1,162 such rows.
Anything that builds a storage key itself will miss all of them — use the
`storage.ObjectKeyCandidates` helper instead.

Legacy images were converted offline with `cwebp` (toggle with `WEBP_FALLBACK`
in `wrangler.toml`). New uploads are converted at ingest, off by default:

```ini
Image__WebpConversionEnabled=true
Image__WebpQuality=90
Image__WebpSourceExtensions=.png
```

The perceptual hash behind duplicate detection is still computed from the
*original* bytes, so re-encoding never weakens dedupe. JPEG is excluded: it's
already lossy, so re-encoding would be lossy-to-lossy.

### Cache invalidation

There is no purge for a `workers.dev` Worker — no zone-level purge reaches it.
Bump `CACHE_VERSION` in
[`scripts/mirror/worker/worker.js`](scripts/mirror/worker/worker.js) and
redeploy; entries are namespaced by it, so the old generation becomes
unreachable and expires on its own.

## 👑 Setting up an admin user

Log in with Discord, then promote the account:

```sql
UPDATE "Users" SET "Role" = 3 WHERE "DiscordId" = 'YOUR_DISCORD_ID';
```

`Role = 3` is Admin (`1` TrustedUser, `2` Moderator). In production, promote via
Adminer or the Supabase dashboard rather than a local `psql`.

## 📚 Further reading

- [`AGENTS.md`](AGENTS.md) — operational hazards: deployment fields, the
  Npgsql connection-string trap, CORS, and why the duplicate threshold is `4`
- [`scripts/mirror/README.md`](scripts/mirror/README.md) — importing a database
  export and mirroring the archive into any S3-compatible bucket
- [`bot/README.md`](bot/README.md) — the sample Discord bot

## Special thanks

- **[Ruhannn](https://github.com/Ruhannn)** — curated the `kamisato-ayaka` tag collection.
- **[Buco7854](https://github.com/Buco7854)** — original author and maintainer for over three years.

## License

[MPL-2.0](LICENSE) — file-level copyleft. Modify freely; publish any file you
change. Forking and self-hosting is explicitly welcome.

Images are third-party artist work, credited per record in the `Images.Source`
column. This repository distributes the catalogue and the metadata, not a claim
on the artwork.

<div align="center">
  <sub>Built with ❤️ by the Waifu.im team and its community fork maintainers.</sub>
</div>
