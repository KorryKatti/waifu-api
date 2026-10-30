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
    <a href="https://discord.gg/qceZ92p2xK"><strong>Discord</strong></a> •
    <a href="https://fluxer.gg/ZyNTpyQR"><strong>Fluxer</strong></a>
  </p>

  <p>
    <a href="https://stats.uptimerobot.com/AbgWCVWsKT"><img src="https://img.shields.io/badge/instance-live-22c55e?style=flat-square" alt="Instance status"></a>
    <img src="https://img.shields.io/github/license/KorryKatti/waifu-api?style=flat-square&color=5865F2" alt="License">
    <img src="https://img.shields.io/badge/Docker-Ready-blue?style=flat-square&logo=docker&logoColor=white" alt="Docker">
  </p>
</div>

> **Community-run fork.** The original project by
> [Buco7854](https://github.com/Waifu-im/waifu-api) shut waifu.im down
> permanently on October 24, 2026. This instance runs in its place and is
> fully operational: the API contract is unchanged and no features are
> unavailable.

## 🚧 Instances

| | |
|---|---|
| API | `https://waifu-api-031n.onrender.com` |
| CDN | `https://waifu-cdn.waifu-api.workers.dev` |
| Website | `https://waifu-api-eosin.vercel.app` |
| Documentation | `https://waifu-api-docs.vercel.app` |
| Uploads | Open. Discord login required; images enter a review queue |
| Users | Discord OAuth works. Existing accounts did not carry over |

### Running your own

Anyone can host their own copy. Image count is deliberately not a signal — one
image is as valid as ten thousand — and there is no score or ranking.

To be listed, an instance must serve `GET /images?pageSize=1` with `items` and
`totalCount` and answer a `HEAD` on the URL that listing returned. The `GET
/version` handshake reports which build it runs; an instance that does not
implement it can still be listed at an operator's request, and shows no uptime.
What each check is for, and what the signals mean, is in
[`docs/docs/instances.mdx`](docs/docs/instances.mdx).

Registration is not self-service. Ask in the
[Discord](https://discord.gg/qceZ92p2xK) or [Fluxer](https://fluxer.gg/ZyNTpyQR)
server, and someone will run `/register` or `!register` when they are around.

## ⚡ Choosing a host

Use the CDN for image bytes and anonymous reads; use the API for `X-Api-Key`
writes. `Authorization: Bearer` works on either. The Worker forwards only
`Authorization`, `Accept` and `Accept-Language`, so an API-key client pointed at
the CDN is unauthenticated and receives `401`.

Anonymous reads are edge-cached for 60s. Authenticated requests, and `/users`,
`/albums`, `/review`, `/auth`, `/reports` and `/stats/admin`, are never cached.

## ✨ Features

- **Archive** — 4,287 images
- **Tags** — character, style and theme filters
- **Albums** — user collections
- **Duplicate detection** — perceptual hashing on upload
- **Filters** — orientation, resolution, file size, NSFW, GIFs, artists, explicit include/exclude
- **Accounts** — Discord OAuth for favourites and albums
- **Moderation** — reports and a review queue
- **Bots** — working Discord and Fluxer samples in [`bot/`](bot/) and [`bot-fluxer/`](bot-fluxer/)
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

## 🏗️ Backend

The service is the Go program in [`backend/go`](backend/go); compose builds it
and it is what runs in production. Build and test commands, including the
database the test suite refuses to run without, are in
[`AGENTS.md`](AGENTS.md#build-and-test).

## ☁️ Hosting

| Concern | Host |
|---|---|
| API | Render — JSON, Postgres |
| Images | Cloudflare Worker — SigV4 to a private bucket, edge-cached |
| Anonymous reads | Cloudflare Worker — proxies to Render, 60s cache |

Storage is private and the Worker signs reads from it; credentials are Worker
secrets, never in `wrangler.toml`. Deploying the Worker and mirroring the archive
are in [`scripts/mirror/README.md`](scripts/mirror/README.md); the WebP
conversion rules are in [`AGENTS.md`](AGENTS.md#webp-quality-85).

No purge reaches a `workers.dev` Worker: bump `CACHE_VERSION` in
[`scripts/mirror/worker/worker.js`](scripts/mirror/worker/worker.js) and
redeploy.

## 👑 Admin access

```sql
UPDATE "Users" SET "Role" = 3 WHERE "DiscordId" = 'YOUR_DISCORD_ID';
```

`1` TrustedUser · `2` Moderator · `3` Admin.

## 📚 Reference

- [`AGENTS.md`](AGENTS.md) — build and test, deployment fields, storage rules, known traps
- [`scripts/mirror/README.md`](scripts/mirror/README.md) — database import, archive mirroring, Worker deploy
- [`docs/docs/instances.mdx`](docs/docs/instances.mdx) — what an instance must expose to be listed
- [`bot/README.md`](bot/README.md) — sample Discord bot
- [`bot-fluxer/README.md`](bot-fluxer/README.md) — sample Fluxer bot

## License

[MIT](LICENSE) — permissive. Forking and self-hosting welcome.

Images are third-party artist work, credited per record in `Images.Source`.

<div align="center">
  <sub>Built with ❤️ by the Waifu.im team and its community fork maintainers.</sub>
</div>
