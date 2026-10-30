# waifu.im Fluxer bot

A prefix-command bot for the [waifu.im API](https://github.com/KorryKatti/waifu-api),
written for [Fluxer](https://fluxer.app) with
[`gofluxer`](https://github.com/go-fluxer/gofluxer).

It is a small port of the Discord bot in `../bot`, not a shared codebase. Fluxer
has no application commands, so every command here is a prefix command. There
are no buttons, no ephemeral replies and no slash commands.

## Run

```bash
cp .env.example .env
# edit .env
go run .
```

The bot reads `.env` from the current directory. Variables already set in the
environment win, so a service manager can supply them instead.

Or build a binary:

```bash
go build -o bot-fluxer .
./bot-fluxer
```

Go 1.23 or newer.

## Commands

| Command | What it does |
| --- | --- |
| `!waifu` | Random SFW image |
| `!waifu maid` | Random image with that tag (multiple tags = AND) |
| `!waifu landscape` | Filter by orientation (`landscape`, `portrait`, `square`) |
| `!waifu gif` | Only animated images |
| `!waifu nsfw` | NSFW, only in an age-restricted channel |
| `!tags` | Tags with image counts |
| `!ping` | API health check and round-trip latency |
| `!stats` | Latency, catalogue size, uptime and errors |
| `!image <id>` | One image with size, format, tags and artist |
| `!tag <slug>` | One of up to five images carrying a tag |
| `!artist <name>` | One of up to five images by an artist |
| `!artists` | Artists with image counts |
| `!version` | Software, version, commit and build time |
| `!instances` | Registered deployments and whether they work |
| `!instances needed` | Re-measure latency now (one an hour, whole server) |
| `!register <url> [name]` | List your own instance on the public index |
| `!help` | List every command; `!help waifu` for one in detail |

Flags combine: `!waifu nsfw gif portrait maid`.

`!tag` and `!artist` fetch five matches and post one at random. That replaces
the Discord gallery's prev/next buttons; run the command again for another
image. `!gallery` is intentionally not ported.

## Hosts

| Traffic | Host |
| --- | --- |
| Every anonymous read (`!waifu`, `!tag`, `!artist`, `!image`, `!tags`, `!artists`, `!ping`, `!version`, `!instances`, `!stats`) | `WAIFU_API`, the Cloudflare Worker |
| `!register` | `WAIFU_ORIGIN`, the API host (required) |

The Worker caches anonymous reads at the edge for 60 seconds and is the default.
It forwards `Authorization` but **drops `X-Api-Key`**, so registration has to go
to the API host. `!register` refuses to send a credential to a `*.workers.dev`
host rather than fail with a silent `401`.

`WAIFU_ORIGIN` has no fallback. If it is unset, `!register` refuses with an
explanation; a read host must never receive the credential by accident. Set it
to the Render origin to use the command.

## NSFW

A channel may serve NSFW when Fluxer marks it age-restricted, or when its channel
ID is listed in `NSFW_CHANNELS`. Being in an age-restricted channel asks for
`isNsfw=all`, not `True`: the channel says what may be seen, not that the reader
wants only NSFW. An explicit `!waifu nsfw` asks for `True`.

Fluxer only sets `nsfw` on top-level guild channels. Threads, group DMs and
channels inheriting the setting from a category have no flag, and `gofluxer`
reads that as SFW. The result is safe but stricter than the Discord bot.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `FLUXER_TOKEN` | required | Bot token |
| `PREFIX` | `!` | Command prefix |
| `WAIFU_API` | the Worker | Anonymous read host |
| `WAIFU_ORIGIN` | empty | API host; required for `!register` |
| `NSFW_CHANNELS` | empty | Comma-separated channel IDs |
| `WAIFU_ADMIN_API_KEY` | empty | Enables `!register` |
| `INSTANCE_OPERATOR_ROLE` | empty | Role granted after a new registration |
| `REGISTER_COOLDOWN_SECONDS` | `3600` | Global registration gap |
| `INSTANCE_PING_SECONDS` | `3600` | Sweep interval; `0` disables it |
| `INSTANCE_PING_COOLDOWN_SECONDS` | `3600` | Global `!instances needed` gap |
| `BOT_STATE_DIR` | `.` | Where the two JSON state files live |

`register_log.json` and `instance_ping_log.json` are local, gitignored and
written through a temporary file and a rename. They keep the cooldown across
restarts.

## Rate limits

`gofluxer` calls `os.Exit(1)` when Fluxer answers `429`. The bot cannot change
that from outside the library, so run it under a supervisor that restarts it:

```ini
[Service]
ExecStart=/path/to/bot-fluxer
Restart=always
```

or, in Compose:

```yaml
restart: unless-stopped
```

## Differences from the Discord bot

- No slash commands or interactions exist on Fluxer.
- No ephemeral replies. Commands that returned account data (`!me`,
  `!permissions`, `!albums`, `!keys`, `!review`, `!reports`, `!adminstats`,
  `!diff`) are not ported, because their answers would be public.
- `!gallery` is not ported; `!tag` and `!artist` post one image instead.
- No typing indicator and no custom status rotation: `gofluxer` exposes neither.
- `!register` sends the caller's Fluxer user ID in the API's existing
  `discordUserId` field.

## Test

```bash
go test ./...
go vet ./...
```
