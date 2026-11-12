<div align="center">
  <a href="https://waifu-api-eosin.vercel.app">
    <img src="https://waifu-cdn.waifu-api.workers.dev/8613.jpg" alt="Waifu.im Logo" width="200" style="border-radius: 20px; box-shadow: 0 8px 30px rgba(0,0,0,0.12);">
  </a>

  <h1>Waifu.im — Fluxer Bot</h1>

  <p>
    <strong>The versatile waifu image provider, on Fluxer.</strong><br>
    Random anime images, tags, artists and instance health — as prefix commands.
  </p>

  <p>
    <a href="https://waifu-api-eosin.vercel.app"><strong>Website</strong></a> •
    <a href="https://waifu-api-docs.vercel.app"><strong>Documentation</strong></a> •
    <a href="https://waifu-api-docs.vercel.app/playground"><strong>Playground</strong></a> •
    <a href="https://discord.gg/qceZ92p2xK"><strong>Discord</strong></a> •
    <a href="https://fluxer.gg/ZyNTpyQR"><strong>Fluxer</strong></a>
  </p>

  <p>
    <img src="https://img.shields.io/badge/platform-Fluxer-5865F2?style=flat-square" alt="Platform">
    <img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Docker-Ready-blue?style=flat-square&logo=docker&logoColor=white" alt="Docker">
    <img src="https://img.shields.io/github/license/KorryKatti/waifu-api?style=flat-square&color=5865F2" alt="License">
  </p>
</div>

A prefix-command bot for the [waifu.im API](https://github.com/KorryKatti/waifu-api),
written for [Fluxer](https://fluxer.app) with
[`gofluxer`](https://github.com/go-fluxer/gofluxer).

It is a small port of the Discord bot in `../bot`, not a shared codebase — the
two bots are independent. Fluxer has no application commands,
so every command here is a prefix command. There are no buttons, no ephemeral
replies and no slash commands.

## ✨ Features

- **Random waifus** — `w!waifu`, with combinable flags for tags, orientation, GIFs and NSFW
- **Catalogue browsing** — `w!tags`, `w!artists`, `w!tag <slug>`, `w!artist <name>`, `w!image <id>`
- **Health & stats** — `w!ping`, `w!stats`, `w!version` (API latency, catalogue totals, uptime)
- **Instance index** — `w!instances` (latency sweep) and `w!register <url> [name]` to list your own deployment
- **Edge-cached reads** — anonymous reads go through the Cloudflare Worker, cached 60s at the edge
- **Docker-first** — multi-stage static build, non-root, read-only, outbound-only (no ports)
- **Persistent cooldowns** — registration / instance-ping cooldowns survive restarts via a named volume

## 🚀 Quick start (Docker, recommended)

```bash
cp .env.example .env
# Edit .env — at minimum set:
#   FLUXER_TOKEN=<your Fluxer bot token>
#   WAIFU_ADMIN_API_KEY=<admin API key>   # only needed for w!register
#   WAIFU_ORIGIN=https://waifu-api-031n.onrender.com

docker compose up -d --build
docker compose logs -f bot-fluxer
```

Then in Fluxer, try:

```
w!ping
w!waifu
w!help
```

### What Compose does

`docker-compose.yml` runs one service, `bot-fluxer` (`waifu-bot-fluxer:latest`):

| Setting | Why |
|---|---|
| `restart: unless-stopped` | `gofluxer` calls `os.Exit(1)` on a Fluxer `429` — the supervisor must restart it (see [Rate limits](#rate-limits)) |
| `env_file: .env` | Secrets live in `.env` on the host, never in the image (`.dockerignore` excludes it) |
| `BOT_STATE_DIR=/data` + volume `bot-fluxer-data:/data` | `register_log.json` / `instance_ping_log.json` persist across restarts and rebuilds |
| `read_only: true` + `tmpfs: /tmp` | The container filesystem is immutable; only `/data` and `/tmp` are writable |
| Limits: 1 CPU / 512 MB (reserve 0.25 CPU / 128 MB) | The bot is a single static binary; this is generous headroom |
| No `ports:` | The bot listens on nothing — it only connects outbound (Fluxer gateway WebSocket + HTTPS APIs) |

### Day-to-day Docker commands

```bash
docker compose logs -f bot-fluxer   # follow logs
docker compose ps                    # container status
docker compose up -d --build         # rebuild + restart after a code change
docker compose restart bot-fluxer    # restart without rebuilding
docker compose down                  # stop (state volume is kept)
docker compose down -v               # stop + wipe the state volume (cooldowns reset)
```

## 🐋 Dockerfile

Multi-stage build producing a small runtime image:

- **Build** (`golang:1.23-alpine`): `go mod download` is cached separately from source, then `CGO_ENABLED=0 GOOS=linux go build -trimpath` produces a static binary with no libc dependency.
- **Runtime** (`alpine:3.20`): only `ca-certificates` (outbound TLS to Fluxer / waifu.im) and `tzdata`. Runs as non-root user `bot` (uid 10001). Alpine — not distroless — so the named volume at `/data` is writable by `bot` without the host pre-creating it root-owned.
- `EXPOSE` is intentionally absent: no inbound traffic exists.

Rebuild from scratch (e.g. after a base-image update):

```bash
docker compose build --no-cache
docker compose up -d
```

## 🖥️ Bare binary (no Docker)

Go 1.23 or newer.

```bash
cp .env.example .env
# edit .env
go run .
```

The bot reads `.env` from the current directory. Variables already set in the
environment win, so a service manager can supply them instead of a file:

```bash
FLUXER_TOKEN=... go run .
```

Or build a binary:

```bash
go build -o bot-fluxer .
./bot-fluxer
```

For a bare binary, `BOT_STATE_DIR` defaults to `.` (see [Configuration](#configuration)).

## ⌨️ Commands

| Command | What it does |
| --- | --- |
| `w!waifu` | Random SFW image |
| `w!waifu maid` | Random image with that tag (multiple tags = AND) |
| `w!waifu landscape` | Filter by orientation (`landscape`, `portrait`, `square`) |
| `w!waifu gif` | Only animated images |
| `w!waifu nsfw` | NSFW, only in an age-restricted channel |
| `w!tags` | Tags with image counts |
| `w!ping` | API health check and round-trip latency |
| `w!stats` | Latency, catalogue size, uptime and errors |
| `w!image <id>` | One image with size, format, tags and artist |
| `w!tag <slug>` | One of up to five images carrying a tag |
| `w!artist <name>` | One of up to five images by an artist |
| `w!artists` | Artists with image counts |
| `w!version` | Software, version, commit and build time |
| `w!instances` | Registered deployments and whether they work |
| `w!instances needed` | Re-measure latency now (one an hour, whole server) |
| `w!register <url> [name]` | List your own instance on the public index |
| `w!help` | List every command; `w!help waifu` for one in detail |

Flags combine: `w!waifu nsfw gif portrait maid`.

`w!tag` and `w!artist` fetch five matches and post one at random. That replaces
the Discord gallery's prev/next buttons; run the command again for another
image. `w!gallery` is intentionally not ported.

Per-user cooldown: `w!waifu`, `w!tag` and `w!artist` allow **one call per user
per 10 seconds**; a repeat inside the window gets a `slow down — try again in …`
reply instead of an API call.

`w!help` is the source of truth in-chat — every command's usage and detail
string lives in `commands.go` (`commandList`), so the table above can drift
but `w!help <command>` cannot.

## 🌐 Hosts — which host to use

| Traffic | Host |
| --- | --- |
| Every anonymous read (`w!waifu`, `w!tag`, `w!artist`, `w!image`, `w!tags`, `w!artists`, `w!ping`, `w!version`, `w!instances`, `w!stats`) | `WAIFU_API`, the Cloudflare Worker |
| `w!register` | `WAIFU_ORIGIN`, the API host (required) |

The Worker caches anonymous reads at the edge for 60 seconds and is the default.
It forwards `Authorization` but **drops `X-Api-Key`**, so registration has to go
to the API host. `w!register` refuses to send a credential to a `*.workers.dev`
host rather than fail with a silent `401`.

`WAIFU_ORIGIN` has no fallback. If it is unset, `w!register` refuses with an
explanation; a read host must never receive the credential by accident. Set it
to the Render origin to use the command.

`w!register` is also gated by guild: it is **disabled everywhere except guild
`1563189593090568192`** (other guilds and DMs). Outside that guild the bot
tells the user it is disabled and names the guild it works in, and no
registration, cooldown or log entry is created. The guild list is
`REGISTER_GUILDS` (comma-separated, defaults to that one guild).

## 🔞 NSFW

A channel may serve NSFW when Fluxer marks it age-restricted, or when its channel
ID is listed in `NSFW_CHANNELS`. Being in an age-restricted channel asks for
`isNsfw=all`, not `True`: the channel says what may be seen, not that the reader
wants only NSFW. An explicit `w!waifu nsfw` asks for `True`.

Fluxer only sets `nsfw` on top-level guild channels. Threads, group DMs and
channels inheriting the setting from a category have no flag, and `gofluxer`
reads that as SFW. The result is safe but stricter than the Discord bot.

If NSFW doesn't work in a channel that *looks* age-restricted, check whether it
is a thread or inherits the setting from a category — then add its channel ID
to `NSFW_CHANNELS` as a workaround. The bot's refusal message quotes the channel
ID to add.

## ⚙️ Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `FLUXER_TOKEN` | required | Bot token |
| `PREFIX` | `w!` | Command prefix |
| `WAIFU_API` | the Worker | Anonymous read host |
| `WAIFU_ORIGIN` | empty | API host; required for `w!register` |
| `REGISTER_GUILDS` | `1563189593090568192` | Comma-separated guild IDs where `w!register` is enabled; disabled elsewhere |
| `NSFW_CHANNELS` | empty | Comma-separated channel IDs |
| `WAIFU_ADMIN_API_KEY` | empty | Enables `w!register` |
| `INSTANCE_OPERATOR_ROLE` | empty | Role granted after a new registration |
| `REGISTER_COOLDOWN_SECONDS` | `3600` | Global registration gap |
| `INSTANCE_PING_SECONDS` | `3600` | Sweep interval; `0` disables it |
| `INSTANCE_PING_COOLDOWN_SECONDS` | `3600` | Global `w!instances needed` gap |
| `BOT_STATE_DIR` | `.` | Where the two JSON state files live |
| `FLUXER_API` | gofluxer default | Optional Fluxer API base override |
| `FLUXER_GATEWAY` | gofluxer default | Optional Fluxer gateway URL override |

`register_log.json` and `instance_ping_log.json` are local, gitignored and
written through a temporary file and a rename. They keep the cooldowns across
restarts. In Docker they live on the `bot-fluxer-data` volume at `/data`; with
a bare binary they live in `BOT_STATE_DIR` (default: the working directory).

Secrets (`FLUXER_TOKEN`, `WAIFU_ADMIN_API_KEY`) are redacted from log lines and
replies via `config.redact` — if a secret ever appears in an error string it is
replaced with `[redacted]` before it reaches Fluxer or the logs.

## 🚦 Rate limits

`gofluxer` calls `os.Exit(1)` when Fluxer answers `429`. The bot cannot change
that from outside the library, so run it under a supervisor that restarts it:

```ini
# systemd (bare binary)
[Service]
ExecStart=/path/to/bot-fluxer
Restart=always
```

or, in Compose (already configured):

```yaml
restart: unless-stopped
```

Symptom if you run it without a supervisor: the process exits on the first
429 and stays down until you start it again. In Docker this shows up as the
container restarting — check `docker compose logs` for the exchange around the
exit.

## 🔁 Differences from the Discord bot

- No slash commands or interactions exist on Fluxer.
- No ephemeral replies. Commands that returned account data (`w!me`,
  `w!permissions`, `w!albums`, `w!keys`, `w!review`, `w!reports`, `w!adminstats`,
  `w!diff`) are not ported, because their answers would be public.
- `w!gallery` is not ported; `w!tag` and `w!artist` post one image instead.
- No typing indicator and no custom status rotation: `gofluxer` exposes neither.
- `w!register` sends the caller's Fluxer user ID in the API's existing
  `discordUserId` field.

## 🛠️ Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `FLUXER_TOKEN is required` on startup | `.env` missing or token empty. With Docker, make sure `docker compose up` picks up the edited `.env` (recreate: `docker compose up -d --force-recreate`). |
| Container keeps restarting | Usually a Fluxer `429` exit (see [Rate limits](#rate-limits)) or an invalid token. `docker compose logs` shows the exchange. |
| `connected to Fluxer` never appears | Check `FLUXER_TOKEN` validity and outbound network to `wss://gateway.fluxer.app`. |
| `w!register` refuses | Needs **both** `WAIFU_ORIGIN` (API host, not the Worker) and `WAIFU_ADMIN_API_KEY`. A `*.workers.dev` origin is refused on purpose — the Worker drops `X-Api-Key`. |
| `w!waifu nsfw` refused in an age-restricted channel | Threads / category-inherited channels have no NSFW flag on Fluxer. Add the channel ID to `NSFW_CHANNELS`, or use a top-level age-restricted channel. |
| Cooldowns reset after a rebuild | The state volume was removed (`docker compose down -v`) or `BOT_STATE_DIR` isn't pointing at the volume. Check `docker volume ls` for `bot-fluxer-data`. |
| `slow down — try again in …` | The 10-second per-user cooldown on `w!waifu` / `w!tag` / `w!artist`. Wait it out. |
| Stale catalogue numbers in `w!stats` | Stats refresh every 30 minutes from `stats/public`. A failed refresh is logged (`stats refresh failed`) and the last reading is kept. |

## 🧪 Test

```bash
go test ./...
go vet ./...
```

## 📁 Layout

| File | What it does |
|---|---|
| `main.go` | Entry point, Fluxer wiring, stats / instance-sweep loops |
| `commands.go` | Prefix dispatch + `commandList` (drives `w!help`) |
| `images.go` | `w!waifu` flag parsing, tag/artist sampling, NSFW gating |
| `instances.go` | `w!instances` latency sweep |
| `register.go` | `w!register` (credential-safe host checks, cooldown) |
| `api.go` | waifu.im API client (timeouts, error redaction) |
| `config.go` / `dotenv.go` | Env loading, defaults, secret redaction |
| `state.go` | In-memory counters + on-disk cooldown state |
| `embeds.go` / `format.go` | Embed building and number/duration formatting |
| `Dockerfile` / `docker-compose.yml` / `.dockerignore` | Container build, orchestration, build-context exclusions |
| `*_test.go` | Unit tests (parsing, config, formatting, state) |

## 🔗 Links

- Parent repo (API + docs): [waifu-api](https://github.com/KorryKatti/waifu-api)
- Discord bot: [`../bot`](../bot/README.md)
- Fluxer library: [gofluxer](https://github.com/go-fluxer/gofluxer)

## 📄 License

See the [LICENSE](../LICENSE) in the parent repository.
