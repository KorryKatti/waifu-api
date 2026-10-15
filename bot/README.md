# waifu.im Discord bot

A minimal bot for the [waifu.im API](https://github.com/KorryKatti/waifu-api).

It talks to the Cloudflare Worker (`https://waifu-cdn.waifu-api.workers.dev`) by
default rather than the API host. The worker caches anonymous reads at the edge
for 60 seconds, so a repeat command is answered from the cache instead of the
database. Set `WAIFU_API` to the API host to bypass the cache.

That default is safe *because this bot is anonymous*. If you adapt it to send
credentials, point `WAIFU_API` at the API host instead — see
[Which host to use](#which-host-to-use) below.

## Which host to use

| What your bot does | Point it at |
|---|---|
| Fetches images (`GET /images`, `/images/sfw`, ...) | `https://waifu-cdn.waifu-api.workers.dev` — edge-cached, and image bytes never touch Render |
| Anything authenticated | `https://waifu-api-031n.onrender.com` — read below first |

Anonymous reads are faster through the Worker, because a repeat of the same
request within 60 seconds is served from the edge instead of the database. The
API host is responsive in its own right — the caching is the advantage, not a
workaround for an unreliable backend.

But the Worker forwards only `Authorization`, `Accept` and `Accept-Language`
(`worker.js:351`). It **drops `X-Api-Key`**, which is one of the two ways this
API authenticates — an API-key client pointed at the Worker gets a silent
`401`, with no error explaining why. Bearer tokens are forwarded correctly,
they just aren't cached; `/users`, `/albums`, `/reports` and friends are never
cached even anonymously.

Use the API host whenever your bot carries any credential.

## Commands

| Command | What it does |
| --- | --- |
| `!help` | List all commands; `!help waifu` for one in detail |
| `!waifu` | Random SFW waifu |
| `!waifu maid` | Random waifu with that tag (multiple tags = AND) |
| `!waifu landscape` | Filter by orientation (`landscape`/`portrait`/`square`) |
| `!waifu gif` | Only animated images |
| `!waifu nsfw` | NSFW, only in an NSFW channel (see below) |
| `!gallery` | Five random images with prev/next buttons, private to the caller |
| `!tags` | List tags with image counts |
| `!ping` | Bot + API health check |
| `!stats` | Live numbers: API latency, catalogue size, uptime, errors |

Flags can be combined: `!waifu nsfw gif portrait maid`.

### Slash commands

These are application commands, so Discord lists them with their own
descriptions and you can find them by typing `/`. They defer before calling the
API, which matters because an interaction has three seconds to answer and a cold
Render instance regularly takes longer than that.

Public — no credential is used, so they are open to the whole server and read
through the edge cache:

| Command | What it does |
| --- | --- |
| `/image id:` | One image, with its tags, size and format |
| `/tag slug:` | Five images carrying a tag, pageable |
| `/artist name:` | Five images by an artist, pageable |
| `/artists` | Artists in the catalogue with image counts |
| `/version` | Which build is serving — software, version, commit, built at |
| `/instances` | Other people's deployments and whether they are serving images |

These send the bot's API key, so they are gated on `WAIFU_DEBUG_USERS` and every
reply is **ephemeral** — visible only to whoever ran them, never left in a
channel:

| Command | What it does |
| --- | --- |
| `/me` | Which account the key belongs to |
| `/permissions` | What that account may do |
| `/albums` | Albums on that account |
| `/keys` | API keys on that account (never plaintext — only hashes are stored) |
| `/review` | What is waiting in the review queue |
| `/reports` | Open reports against images |
| `/adminstats` | Operator dashboard numbers |
| `/diff` | Rows with no matching object in storage |

With `WAIFU_DEBUG_USERS` empty — the default — the second table refuses and
says which variable to set. It is an allowlist rather than a permission or a
role because the credential can delete images, approve review tasks and ban
accounts, and either of the other two can be handed to somebody who then holds an
admin credential by accident. See [Config](#config).

There is deliberately no command that takes a raw method and path. The key can
delete things, this bot runs in servers with other people in it, and a command
that forwards whatever it is typed is the wrong shape for that. These commands
cover the read surface; writes are done from the dashboard or the API directly.

### `/register`

A slash command rather than a prefix command, because it registers an instance
on the public index and is not something to trigger in a public channel:

```
/register url:https://waifu.example name:My Instance
```

The backend probes the instance before storing it - handshake, catalogue shape,
and one image fetched over its own URL - and refuses it if any of those fail, so
the index cannot fill up with links that 404. An instance with an empty catalogue
is listed but its image signal stays unsatisfied, because "correct and empty" and
"correct and serving" are different claims.

The reply is ephemeral and deferred: the probe is three requests to someone
else's server, which is longer than Discord's acknowledgement window.

Running it twice is safe and does not overwrite the entry. That matters more than
it sounds: an overwrite would reset the check history, which reports a fresh
week of perfect uptime for an instance that has actually been failing.

The bot sends its own admin credentials and the caller's numeric Discord id. It
never sends a username - usernames still resolve to a different account after a
rename, so keying an entry or a role on one hands both to whoever took the name.

Set `WAIFU_ADMIN_API_KEY` and `INSTANCE_OPERATOR_ROLE` to enable it; see
[Config](#config).

### The admin key

`/register` needs the bot to be allowed to write, so the bot holds a key belonging
to an admin account. While logged in as an admin:

```
POST /auth/api-keys   {"description": "waifu bot", "expiresInDays": 0}
```

`expiresInDays: 0` means it does not expire. The plaintext is returned once and
only its hash is stored, so a lost key is replaced rather than recovered, and it
is deleted from the same screen.

An API key rather than a JWT, because a JWT would need re-minting every 24 hours
and the bot would fail quietly when it lapsed. A key is revocable, and
revocation is the property that matters for something sitting in an environment
file for the life of the bot.

The bot sends it as `X-Api-Key`. It must not go through the Worker: the Worker
forwards `Authorization` but drops `X-Api-Key`, so a key sent that way is
silently anonymous and every registration fails as forbidden.

## The status line

The bot rotates a presence line every 8–13 minutes. Some are jokes; the rest
report something measured:

| Slot | Shows |
| --- | --- |
| every 3rd | A joke |
| every 4th | Whatever the last command was doing, for 30 minutes after it |
| live | API latency, as measured against the configured host |
| live | Catalogue size, cycled across images/tags/artists |
| live | Uptime and images served, or how long ago the last API error was |

The live lines come from a background probe that hits `/ping` every 60 seconds
and `/stats/public` every 30 minutes, so they stay current whether or not anyone
is running a command. Counts come from the same endpoint, so they are never
hardcoded. A live line with nothing to report falls back to a joke, so the
presence is never blank on a cold start or during an outage.

`!stats` shows the same numbers on demand.

## NSFW channels

`nsfw` is refused everywhere except a channel that is age-restricted. A channel
qualifies if either:

- Discord marks it age-restricted (*Channel Settings > Age Restriction*), which also
  makes Discord apply its own age gate, **or**
- its ID is listed in `NSFW_CHANNELS` in the bot config.

Using Discord's own setting is the stronger option — it gates non-bot members too.
`NSFW_CHANNELS` is a fallback for servers where you can't flip that setting.

If someone tries `!waifu nsfw` in a normal channel, the bot refuses and tells them
which channel ID to add.

This is not a rule only `!waifu nsfw` obeys. **Every command that returns images
applies it**, and does so automatically — `/artist`, `/tag`, `!gallery` and
`/image` read the channel rather than waiting to be told, so a normal channel
gets SFW-only results with no extra flag and an age-restricted one gets NSFW
without being asked.

Permission and request are different things, so they take different values. A
channel that is allowed to show NSFW is sent `isNsfw=all`, because being allowed
to see something is not a request for it: an artist's catalogue is normally
mixed, and filtering to NSFW-only would hide the SFW half of their work from the
one channel that is allowed to show it. It would also quietly break the gallery
— an artist with fewer NSFW images than the page size returns a short page and a
"next" button that wraps through nothing. Explicit `!waifu nsfw` still sends
`isNsfw=True`, since that *is* someone asking for NSFW specifically.

That automatic behaviour is why the rule lives in the channel and not in the
command. `GET /images` defaults to SFW-only when `isNsfw` is absent, so a command
that forgets to send the filter is not a leak — it just silently under-reports,
which looks exactly like a broken bot in an NSFW channel. It is the other
direction that gets a bot removed: `GET /images/{id}` has no NSFW filter at all,
so `/image <id>` is gated by the bot alone.

## Setup

```bash
uv venv
uv pip install -r requirements.txt
cp .env.example .env   # then fill in DISCORD_TOKEN
uv run python bot.py
```

## Getting a token

1. Create an app at https://discord.com/developers/applications
2. **Bot** → **Reset Token** → copy it into `DISCORD_TOKEN` in `.env`
3. **Bot** → **Privileged Gateway Intents** → turn **MESSAGE CONTENT** on.
   Without this the bot connects but silently ignores every command.
4. Invite the bot to your server with the `bot` scope and `Send Messages` permission

## Config

| Var | Default | Notes |
| --- | --- | --- |
| `DISCORD_TOKEN` | required | Bot token |
| `PREFIX` | `!` | Command prefix |
| `WAIFU_API` | the Cloudflare Worker | Use the API host instead if your bot sends credentials |
| `NSFW_CHANNELS` | empty | Extra channel IDs allowed to get NSFW results (adds to Discord's own age-restricted channels) |
| `WAIFU_DEBUG_USERS` | empty | Discord account ids allowed to run the credentialed slash commands. Empty disables them |
| `WAIFU_API_TEST_KEY` | `WAIFU_ADMIN_API_KEY` | Credential those commands send. Point it at a lesser account |

### The rate limit

One registration per hour, **across the whole server** rather than per person.
The thing being limited is not the caller, it is the backend: the API probes the
instance with three outbound requests before storing anything, using a credential
the bot holds and nobody else has.

Cooldown length depends on the outcome:

| Outcome | Cooldown | Why |
| --- | --- | --- |
| Registered, or already registered | 1 hour | That is the thing being limited |
| `422` — the instance did not answer | 1 hour | Three outbound requests happened |
| `401` / `403` | 1 hour | A misconfiguration, not the caller's fault; without a cooldown everybody who tries it hammers a broken credential |
| `400` — a malformed URL or a username instead of an id | **none** | Rejected before the prober runs, so it costs nothing and a typo should not cost an hour |
| The API was unreachable | **none** | Not the caller's fault; they should retry when it is back |

The state and a short history live in `register_log.json`, next to the bot and
gitignored — the limiter has to survive a restart, or restarting the bot becomes
a way around it. Deleting the file resets it, which is the one hole in it.
