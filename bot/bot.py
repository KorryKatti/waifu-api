import os
import logging
import math
import asyncio
import random
import time
import json
import urllib.parse
import traceback
from datetime import datetime, timezone

import discord
from discord import app_commands
from discord.ext import commands
import aiohttp
from dotenv import load_dotenv

load_dotenv()

# Default to the Cloudflare Worker rather than the API host directly.
#
# The worker caches anonymous reads at the edge for 60s, so a repeat command is
# answered from the cache instead of the database. The API host is responsive in
# its own right; the caching is the advantage, not a workaround for a sleeping
# backend. The response shape is identical either way.
API_BASE = os.getenv("WAIFU_API", "https://waifu-cdn.waifu-api.workers.dev").rstrip("/")

# The API host itself, used only by the keep-alive probe.
#
# Probing API_BASE does not keep Render awake, and that is the point of this
# variable. /ping is not on the worker's NEVER_CACHE list, so it is served from
# the edge for JSON_TTL_SECONDS (60s) - and the probe interval is also 60s. Two
# equal periods means whether a given probe reaches the origin is decided by
# sub-second timing jitter: land after the entry expires and it forwards, land
# before and the worker answers from cache and Render never sees the request at
# all. The keep-alive works roughly half the time.
#
# Going straight to the host removes the cache from the path entirely, so every
# probe lands. Falls back to API_BASE, which is correct for a bot running
# against an uncached origin already.
ORIGIN_BASE = os.getenv("WAIFU_ORIGIN", API_BASE).rstrip("/")

PREFIX = os.getenv("PREFIX", "!")

# Ordinary API calls give up after 30s. The keep-alive probe overrides this,
# because a cold start on a free Render instance takes longer than that and a
# timeout expiring mid-wake scores a healthy API as an error.
DEFAULT_TIMEOUT_SECONDS = 30
PROBE_TIMEOUT_SECONDS = 90

# Registration is slower than an ordinary read: the backend probes the instance
# before storing it, which is three requests to someone else's server. The
# interaction has already been deferred by this point, so a long wait is only
# visible as a slow reply rather than as a failed command.
REGISTER_TIMEOUT_SECONDS = 60
NSFW_CHANNELS = {int(c) for c in os.getenv("NSFW_CHANNELS", "").split(",") if c.strip().lstrip("-").isdigit()}

# Registration credentials for /register.
#
# Every other command here is anonymous, because every other endpoint is. This
# one is not: registering an instance is admin-gated, because the backend makes
# an outbound HTTP request to whatever URL it is given. So this belongs to an
# admin account, and the bot is the only writer of the index.
#
# An API key rather than a JWT, deliberately. A JWT is signed with the service's
# signing key and expires in 24 hours, so a bot holding one needs a new one daily
# and fails quietly when it lapses. An API key is stored hashed, is revocable from
# the dashboard, and does not expire unless it is created with an expiry - which
# is what a service credential should be. It is revocable rather than expiring,
# because revocation is the property that matters to a credential sitting in an
# environment file for the life of the bot.
#
# Create it once, as an admin, with:
#   POST /auth/api-keys  {"description": "waifu bot", "expiresInDays": 0}
# The plaintext is returned once and cannot be recovered.
#
# Point it at the API host rather than the Worker: the Worker forwards
# Authorization but drops X-Api-Key, so a key sent through it is silently
# anonymous.
ADMIN_API_KEY = os.getenv("WAIFU_ADMIN_API_KEY", "").strip()

# Who may drive the authenticated API-testing commands.
#
# The bot holds an admin key, so anything that can reach these commands can
# delete images, ban accounts and approve review tasks. That is fine for the
# operator and not fine for a server with other people in it, so access is an
# explicit allowlist of Discord account ids rather than a permission or a role.
#
# Empty means every one of these commands refuses and says why. It does not mean
# open: defaulting to permissive would mean a bot deployed without reading this
# comment is one delete away from an incident.
API_DEBUG_USERS = {
    int(u)
    for u in os.getenv("WAIFU_DEBUG_USERS", "").split(",")
    if u.strip().lstrip("-").isdigit()
}

# The key the testing commands authenticate with.
#
# Defaults to the admin key so a working /register setup needs no extra
# configuration, but it can be pointed at a lesser account. Worth doing: the
# testing commands exist to poke at read paths, and an admin key is not needed
# for that.
API_TEST_KEY = os.getenv("WAIFU_API_TEST_KEY", "").strip() or ADMIN_API_KEY

# Longest API response rendered into a Discord message.
#
# Discord rejects a message over 2000 characters outright, and an embed field
# over 1024, so an unpaginated listing endpoint produces a response the bot
# cannot send. Truncating keeps `!api GET /images` useful for inspection instead
# of turning every large response into a 400 from Discord that looks like the
# API failed.
MAX_RESPONSE_CHARS = 1700

# Where /register posts, which is never the CDN.
#
# The Worker drops X-Api-Key: buildUpstreamHeaders forwards only Authorization,
# Accept and Accept-Language. A registration sent through it therefore arrives
# anonymous and the API answers 401, and the caller is told only that "the API
# rejected the registration" - a failure that points at nothing.
#
# That is not hypothetical. WAIFU_ORIGIN is unset in a default .env, and both
# API_BASE and ORIGIN_BASE then resolve to the Worker, so the first real
# /register silently posted a credential into a proxy that discards it.
#
# Set WAIFU_ORIGIN to the API host - the Render origin, not the CDN.
REGISTER_TARGET = ORIGIN_BASE


def register_target_is_cdn(target: str) -> bool:
    """Whether this target would discard the credential we authenticate with.

    Checked by host rather than by comparing against API_BASE, because the CDN
    and the origin can both be configured and the one that matters is whichever
    this is.
    """
    host = urllib.parse.urlparse(target).hostname or ""
    return host.endswith("workers.dev") or host == "workers.dev"


def redact(text: str) -> str:
    """Strip every configured credential from any string on to a log or a message.

    Both the admin key and the separate testing key are scrubbed, not just the
    first. WAIFU_API_TEST_KEY falls back to WAIFU_ADMIN_API_KEY when unset but is
    not required to, and a scrubber that only knew about the admin key would let
    the other one through into a log line in exactly the configuration where
    somebody deliberately made them different.

    No code path here puts a key into a third-party string: request headers are
    never logged and the key is sent as a header. So today this is defence in
    depth rather than a fix for an observed leak.

    It stays because the cost of being wrong is a permanent, non-expiring write
    credential in somebody's log file, and because an HTTP library putting
    request headers into an exception message is exactly the kind of thing that
    changes without warning in a patch release. Scrubbing at the boundary means
    that possibility costs nothing.
    """
    for secret in {ADMIN_API_KEY, API_TEST_KEY}:
        if secret and secret in text:
            text = text.replace(secret, "[redacted]")
    return text

# The role granted to somebody whose instance is on the index. Empty means no
# grant, and the command says so rather than appearing to have worked.
OPERATOR_ROLE_ID = os.getenv("INSTANCE_OPERATOR_ROLE", "").strip()

# One registration per hour, across the whole server.
#
# Global rather than per-person, because the thing being limited is not the
# person: it is the backend, which probes the instance with three outbound
# requests before storing anything, using a credential the bot holds and nobody
# else has. Per-user limits would let a channel-full of members make the same
# number of requests in the same minute, which is the opposite of the point.
#
# Cooldown length differs by outcome, which is what makes it fair rather than
# merely safe:
#
#   - 400, a malformed URL or a username instead of an id. Rejected before the
#     prober runs, so it costs the backend nothing and costs the caller nothing
#     to retry. No cooldown.
#   - 422, the probe failed. Three outbound requests happened, so the full
#     cooldown applies - and the caller is told when they can try again.
#   - 200 and 201, a registration. Exactly what this is here to limit.
#   - 401 and 403, a misconfiguration rather than anything the caller did. Full
#     cooldown, or everybody who tries it would hammer a broken credential.
REGISTER_COOLDOWN_SECONDS = int(os.getenv("REGISTER_COOLDOWN_SECONDS", "3600"))

# Where the command's history lives: who ran it, for what, and what came back.
#
# The limiter has to survive a restart, or restarting the bot becomes a way
# around it, and a file is the smallest thing that does that without adding a
# dependency. Gitignored, contains Discord ids, stays local.
#
# Writes go through a temp file and a rename. A half-written file would read back
# as a crash on the next start, and the symptom would read as "the limiter is
# broken" rather than "the disk was full".
REGISTER_LOG_PATH = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "register_log.json"
)

# Older entries are dropped. This is a rate limiter's state and a short history,
# not an audit log, and an unbounded file next to the code is a nuisance.
REGISTER_LOG_LIMIT = 200

# Serialises check, then register, then write. Holding it across the network call
# is deliberate: without it two people running the command in the same second
# would both read an empty cooldown, both pass, and both register.
_register_lock = asyncio.Lock()


def load_register_log() -> dict:
    """Read the log, treating anything unreadable as empty.

    A corrupt file must not brick the command. The worst outcome of starting
    empty is one registration with no recorded cooldown, which is also what
    happens the first time the command is ever used.
    """
    try:
        with open(REGISTER_LOG_PATH, encoding="utf-8") as handle:
            data = json.load(handle)
    except FileNotFoundError:
        return {"lastCooldownAt": None, "entries": []}
    except (OSError, ValueError) as error:
        log.warning("register log unreadable, starting a new one: %s", error)
        return {"lastCooldownAt": None, "entries": []}

    if not isinstance(data, dict):
        return {"lastCooldownAt": None, "entries": []}
    data.setdefault("lastCooldownAt", None)
    data.setdefault("entries", [])
    return data


def save_register_log(data: dict) -> None:
    """Write the log atomically.

    Failures are logged and swallowed: losing the history is annoying, and
    refusing to register an instance because a file could not be written is
    worse.
    """
    data["entries"] = data["entries"][-REGISTER_LOG_LIMIT:]

    temporary = f"{REGISTER_LOG_PATH}.tmp"
    try:
        with open(temporary, "w", encoding="utf-8") as handle:
            json.dump(data, handle, indent=2)
        os.replace(temporary, REGISTER_LOG_PATH)
    except OSError as error:
        log.warning("could not write the register log: %s", error)
        try:
            os.unlink(temporary)
        except OSError:
            pass


def register_cooldown_remaining(data: dict, now: datetime) -> float:
    """Seconds until the next registration is allowed, or zero."""
    raw = data.get("lastCooldownAt")
    if not raw:
        return 0.0
    try:
        last = datetime.fromisoformat(raw)
    except (TypeError, ValueError):
        return 0.0

    return max(0.0, REGISTER_COOLDOWN_SECONDS - (now - last).total_seconds())


def describe_wait(seconds: float) -> str:
    """A wait long enough to be worth saying, short enough not to need a clock."""
    minutes = int(seconds // 60)
    if minutes < 1:
        return "less than a minute"
    if minutes < 60:
        return f"{minutes} minute{'s' if minutes != 1 else ''}"

    hours, remainder = divmod(minutes, 60)
    return f"{hours} hour{'s' if hours != 1 else ''}" + (
        f" {remainder} min" if remainder else ""
    )


def record_attempt(data: dict, interaction, url, name, outcome, detail) -> None:
    """Append one attempt to the history."""
    data["entries"].append(
        {
            "at": datetime.now(timezone.utc).isoformat(),
            "discordId": str(interaction.user.id),
            "username": str(interaction.user),
            "guild": interaction.guild.name if interaction.guild else None,
            "url": url,
            "name": name,
            "outcome": outcome,
            "detail": detail,
        }
    )
    log.info("register attempt: %s from %s for %s", outcome, interaction.user, redact(url))

logging.basicConfig(level=logging.INFO)
log = logging.getLogger("waifu")

intents = discord.Intents.default()
# Prefix commands read message text, so the MESSAGE CONTENT privileged intent must be
# switched on in the Discord developer portal (Bot > Privileged Gateway Intents).
intents.message_content = True

session: aiohttp.ClientSession | None = None
ORIENTATIONS = {"landscape", "portrait", "square"}


class WaifuApiError(Exception):
    """The API answered, but with an error status.

    `status` is the HTTP code and `detail` is the problem+json detail, which is
    the part worth showing a person: for a failed registration it names which of
    the instance's checks did not answer.
    """

    def __init__(self, message, status=None):
        super().__init__(message)
        self.status = status
        self.detail = message


def nsfw_allowed(channel):
    """A channel may serve NSFW results if Discord itself age-gates it, or if its
    ID is in the NSFW_CHANNELS allowlist (for channels the admin didn't mark)."""
    return channel.is_nsfw() or channel.id in NSFW_CHANNELS


def nsfw_hint(channel):
    return (
        f"This channel isn't marked age-restricted. Ask a server admin to enable "
        f"*Channel Settings > Age Restriction*, or add the channel ID "
        f"({channel.id}) to `NSFW_CHANNELS` in the bot config."
    )


def command_usage(prefix, cmd):
    args = cmd.usage or cmd.signature or ""
    return f"{prefix}{cmd.qualified_name} {args}".strip()


class WaifuHelp(commands.HelpCommand):
    """!help — list commands, or !help <command> for details."""

    async def send_bot_help(self, mapping):
        ctx = self.context
        embed = discord.Embed(
            title=f"{ctx.me.name} — commands",
            description="Powered by the [waifu.im](https://waifu-api-eosin.vercel.app) API.",
            color=discord.Color.magenta(),
        )
        for cmd in sorted(self.context.bot.commands, key=lambda c: c.name):
            if cmd.hidden or not cmd.short_doc:
                continue
            embed.add_field(
                name=f"`{command_usage(ctx.clean_prefix, cmd)}`", value=cmd.short_doc, inline=False
            )
        embed.set_footer(text=f"Run {ctx.clean_prefix}help <command> for details.")
        await ctx.send(embed=embed)

    async def send_command_help(self, command):
        ctx = self.context
        signature = command_usage(ctx.clean_prefix, command)
        embed = discord.Embed(
            title=f"`{signature}`",
            description=command.help or "No description.",
            color=discord.Color.magenta(),
        )
        if command.aliases:
            embed.add_field(name="Aliases", value=", ".join(f"`{a}`" for a in command.aliases))
        await ctx.send(embed=embed)


bot = commands.Bot(command_prefix=PREFIX, intents=intents, help_command=WaifuHelp())

# Slash commands hang off bot.tree, which commands.Bot has created since
# discord.py 2.0. Assigning a fresh CommandTree here raises ClientException -
# "this client already has an associated command tree" - so it is used as-is.
#
# /register is a slash command rather than a prefix command because it is an
# operator action rather than something anybody should be able to trigger in a
# public channel with a prefix, and because Discord's interaction carries the
# caller's identity: that is how the registration records who the instance
# belongs to without asking anybody to type their own user id.

# When the process started, for the uptime line.
STARTED_AT = time.monotonic()

# Numbers the status line and !stats report, gathered as the bot goes about its
# business rather than by asking the API when someone is watching.
#
# Written by the probe task, by every api_get, and by the commands that hand an
# image out; read by current_status. Every value is optional except the counters:
# a line that has nothing to show returns None and falls back to a fixed one.
STATS = {
    "latency_ms": None,      # last successful round trip to API_BASE
    "images": None,          # /stats/public totalImages
    "tags": None,            # /stats/public totalTags
    "artists": None,         # /stats/public totalArtists
    "served": 0,             # images this process has posted
    "last_id": None,         # ID of the most recent image posted
    "last_served_at": None,  # monotonic, or None if nothing has been served
    "errors": 0,             # failed API calls since start
    "last_error_at": None,   # monotonic, or None if nothing has failed yet
}


def note_ok(latency_ms):
    STATS["latency_ms"] = round(latency_ms)


def note_error():
    STATS["errors"] += 1
    STATS["last_error_at"] = time.monotonic()


def note_served(img):
    STATS["served"] += 1
    STATS["last_id"] = img.get("id")
    STATS["last_served_at"] = time.monotonic()


async def api_get(path, *, base=None, timeout=None, **params):
    """GET a path from the API host, returning the decoded body.

    Every API call goes through here so latency and errors are recorded wherever
    they happen, instead of only in the places that remember to.

    The body is JSON when the response says it is and text otherwise: /ping
    answers `pong` as text/plain, so decoding unconditionally would turn a
    healthy API into a raised ContentTypeError.

    base and timeout are keyword-only so they can never be mistaken for query
    parameters: `timeout=5` meant as a query value would otherwise be swallowed
    as the request timeout instead of being sent.
    """
    global session
    sess = session
    if sess is None or sess.closed:
        sess = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=DEFAULT_TIMEOUT_SECONDS))
    # urlencode, not string joining: a tag containing a space, "&" or "#"
    # would otherwise produce a malformed URL or, worse, silently inject an
    # extra parameter. Today's slugs are all lowercase and hyphenated so nothing
    # trips it yet, but tags come from whatever users upload.
    query = urllib.parse.urlencode(
        {k: v for k, v in params.items() if v not in (None, "", [])}
    )
    url = f"{base or API_BASE}/{path.lstrip('/')}"
    if query:
        url = f"{url}?{query}"
    started = time.monotonic()
    try:
        # Per request, not per session: the session is built once in on_ready,
        # so a session-level timeout would apply to whichever call happened to
        # create it and to nothing afterwards.
        async with sess.get(
            url,
            timeout=aiohttp.ClientTimeout(total=timeout or DEFAULT_TIMEOUT_SECONDS),
        ) as resp:
            if resp.status >= 400:
                note_error()
                raise WaifuApiError(
                    f"The waifu API returned HTTP {resp.status}.", status=resp.status
                )
            if resp.content_type == "application/json":
                body = await resp.json(content_type=None)
            else:
                body = await resp.text()
            note_ok((time.monotonic() - started) * 1000)
            return body
    except WaifuApiError:
        raise
    except Exception:
        note_error()
        raise
    finally:
        if sess is not session and not sess.closed:
            await sess.close()
        elif sess.closed:
            session = None


async def api_post_instances(url, *, name=None, discord_user_id=""):
    """Register an instance through POST /instances.

    Returns (status, body). The status is the part that matters: 201 is a new
    entry and 200 is an existing one, re-running unchanged. The endpoint does not
    say which in the payload, and inferring it from the body would mean guessing
    at a field that may or may not be present.

    Discord's numeric account id is sent rather than a username. Usernames are
    mid-migration to globally-unique and still resolve to a different account
    after a rename, so keying the entry or the operator role on one would hand
    both to whoever took the name.
    """
    global session
    sess = session
    if sess is None or sess.closed:
        sess = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=DEFAULT_TIMEOUT_SECONDS))

    payload = {"url": url, "name": name or "", "discordUserId": discord_user_id}
    if register_target_is_cdn(REGISTER_TARGET):
        raise WaifuApiError(
            "The bot is configured to send registrations to the CDN, which drops "
            "the API key, so they would be rejected. Set WAIFU_ORIGIN to the API "
            "host in .env - the Render origin, not the CDN.",
            status=0,
        )

    endpoint = f"{REGISTER_TARGET}/instances"

    try:
        async with sess.post(
            endpoint,
            json=payload,
            timeout=aiohttp.ClientTimeout(total=REGISTER_TIMEOUT_SECONDS),
            headers={"X-Api-Key": ADMIN_API_KEY},
        ) as resp:
            body = await resp.json(content_type=None) if resp.content_type == "application/json" else {}
            if resp.status >= 400:
                note_error()
                # The problem+json detail is the whole value of this call: it says
                # which of the instance's checks failed, and "the API returned an
                # error" would leave the operator guessing.
                detail = body.get("detail") if isinstance(body, dict) else None
                raise WaifuApiError(detail or "The API rejected the registration.", status=resp.status)
            note_ok(0)
            return resp.status, body
    except WaifuApiError:
        raise
    except Exception:
        note_error()
        raise
    finally:
        if sess is not session and not sess.closed:
            await sess.close()


async def fetch_images(**params):
    return await api_get("images", **params)


async def api_request(method, path, *, params=None, body=None, auth=False, base=None, timeout=None):
    """Call any endpoint and return (status, decoded_body, content_type).

    api_get exists for the anonymous read path and raises on 4xx/5xx, which is
    the right behaviour for a command that either renders a result or explains
    a failure. This one does neither, because the point of the testing commands
    is to *see* what came back, including the 401 and the problem+json body that
    says why. Raising would throw away exactly the information they exist for.

    auth=True sends X-Api-Key, which only the API host forwards. The Worker drops
    it (worker.js buildUpstreamHeaders keeps Authorization, Accept and
    Accept-Language and nothing else), so an authenticated call routed there
    arrives anonymous and every result reads 401. Rather than let that look like
    an API fault, it is refused with the reason.
    """
    global session
    sess = session
    if sess is None or sess.closed:
        sess = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=DEFAULT_TIMEOUT_SECONDS))

    target = (base or ORIGIN_BASE).rstrip("/")

    if auth and register_target_is_cdn(target):
        raise WaifuApiError(
            "Refusing to send credentials to a CDN host: it drops X-Api-Key, so "
            "the call would arrive anonymous and read as a 401. Set "
            "WAIFU_ORIGIN to the API host.",
            status=0,
        )

    headers = {"X-Api-Key": API_TEST_KEY} if auth and API_TEST_KEY else {}
    url = f"{target}/{path.lstrip('/')}"
    if params:
        query = urllib.parse.urlencode({k: v for k, v in params.items() if v not in (None, "", [])})
        if query:
            url = f"{url}?{query}"

    kwargs = {"headers": headers}
    if body is not None:
        kwargs["json"] = body

    started = time.monotonic()
    try:
        async with sess.request(
            method.upper(), url,
            timeout=aiohttp.ClientTimeout(total=timeout or DEFAULT_TIMEOUT_SECONDS),
            **kwargs,
        ) as resp:
            ctype = resp.content_type
            # Every error from this API is application/problem+json, not
            # application/json, so matching on the exact type would hand back a
            # raw string for precisely the responses worth reading. The suffix
            # check is what makes the problem+json detail — which says *why*
            # something was refused — available to the caller.
            if ctype.endswith("json"):
                payload = await resp.json(content_type=None)
            else:
                payload = await resp.text()
            if resp.status >= 400:
                note_error()
            else:
                note_ok((time.monotonic() - started) * 1000)
            return resp.status, payload, ctype
    finally:
        if sess is not session and not sess.closed:
            await sess.close()


def format_response(status, payload, ctype):
    """Render an API response into something Discord will accept.

    Keyed off the payload's type rather than the content type: by the time this
    is called the body is already decoded, and a problem+json error arrives as a
    dict under a content type that is not application/json. Pretty-printing by
    content type would render every error as a Python repr.
    """
    if isinstance(payload, (dict, list)):
        try:
            rendered = json.dumps(payload, indent=2, ensure_ascii=False, default=str)
        except (TypeError, ValueError):
            rendered = repr(payload)
    else:
        rendered = str(payload)

    rendered = redact(rendered)
    if len(rendered) > MAX_RESPONSE_CHARS:
        # Keep the tail, not the head: the interesting part of a large listing is
        # its shape and its last entries, and a response that opens with 1700
        # characters of the first page tells you nothing about the rest.
        rendered = (
            f"...truncated, {len(rendered)} chars total, showing the end...\n"
            f"{rendered[-MAX_RESPONSE_CHARS:]}"
        )
    return rendered


def debug_allowed(user):
    """Whether this caller may run the API-testing commands, and why not if not.

    Takes the user rather than a context so the slash commands, which receive
    an Interaction and have no `.author`, can use the same gate as anything
    else. Both expose `.id`.
    """
    if not API_DEBUG_USERS:
        return (
            False,
            "These commands are disabled: `WAIFU_DEBUG_USERS` is empty in the bot's "
            ".env. Set it to your Discord account id to enable them.",
        )
    if user.id not in API_DEBUG_USERS:
        return False, "You're not in `WAIFU_DEBUG_USERS`."
    if not API_TEST_KEY:
        return (
            False,
            "No credential is configured. Set `WAIFU_API_TEST_KEY` (or "
            "`WAIFU_ADMIN_API_KEY`) to an API key.",
        )
    return True, ""


def build_embed(img):
    artist = next((a.get("name") for a in img.get("artists") or []), None)
    tags = [t.get("name") for t in img.get("tags") or []]
    embed = discord.Embed(color=discord.Color.magenta())
    embed.set_image(url=img["url"])
    embed.add_field(name="ID", value=str(img["id"]))
    if tags:
        embed.add_field(name="Tags", value=", ".join(tags))
    if artist:
        embed.add_field(name="Artist", value=artist)
    if img.get("isAnimated"):
        embed.add_field(name="Format", value="GIF")
    if img.get("source"):
        embed.add_field(name="Source", value=f"[link]({img['source']})")
    embed.set_footer(text="waifu.im")
    return embed


@bot.command(name="waifu")
@commands.cooldown(1, 10, commands.BucketType.user)
async def waifu(ctx, *, query: str = ""):
    """Get a waifu. Optionally pass `nsfw`, `gif`, an orientation, or tag names."""
    parts = query.lower().split()

    is_nsfw = "nsfw" in parts or nsfw_allowed(ctx.channel)
    if "nsfw" in parts and not nsfw_allowed(ctx.channel):
        await ctx.reply(f"This channel isn't an NSFW channel. {nsfw_hint(ctx.channel)}")
        return
    if "nsfw" in parts:
        parts.remove("nsfw")

    params = {
        "isNsfw": "True" if is_nsfw else "False",
        "isAnimated": "All",
        "orderBy": "Random",
        "pageSize": 1,
    }

    if "gif" in parts:
        parts.remove("gif")
        params["isAnimated"] = "True"

    orientation = next((p for p in parts if p in ORIENTATIONS), None)
    if orientation:
        parts.remove(orientation)
        params["orientation"] = orientation.capitalize()

    if parts:
        # Comma, not a dash: the API splits this parameter on commas, so a
        # dash-joined "ayaka-swimsu" arrives as one tag that matches nothing and
        # the user gets "No images matched that search". Invisible until the
        # parameter stopped being silently dropped.
        params["includedTags"] = ",".join(parts)

    async with ctx.typing():
        try:
            data = await fetch_images(**params)
        except WaifuApiError as e:
            await ctx.reply(redact(str(e)))
            return
        except Exception as e:
            log.error("%s", redact(traceback.format_exc()))
            await ctx.reply(f"Could not reach the waifu API: {redact(str(e))}")
            return

    items = data.get("items") or []
    if not items:
        await ctx.reply("No images matched that search.")
        return
    note_served(items[0])
    await ctx.send(embed=build_embed(items[0]))


class GalleryView(discord.ui.View):
    def __init__(self, images, user):
        super().__init__(timeout=180)
        self.images = images
        self.index = 0
        self.user = user

    async def interaction_check(self, interaction: discord.Interaction) -> bool:
        if interaction.user.id != self.user.id:
            await interaction.response.send_message("Not your gallery.", ephemeral=True)
            return False
        return True

    @discord.ui.button(label="◀ Prev", style=discord.ButtonStyle.secondary)
    async def prev_button(self, interaction: discord.Interaction, button: discord.ui.Button):
        self.index = (self.index - 1) % len(self.images)
        await interaction.response.edit_message(embed=build_embed(self.images[self.index]), view=self)

    @discord.ui.button(label="▶ Next", style=discord.ButtonStyle.secondary)
    async def next_button(self, interaction: discord.Interaction, button: discord.ui.Button):
        self.index = (self.index + 1) % len(self.images)
        await interaction.response.edit_message(embed=build_embed(self.images[self.index]), view=self)

    async def on_timeout(self):
        for item in self.children:
            item.disabled = True
        msg = await self.message.channel.fetch_message(self.message.id)
        await msg.edit(view=self)


@bot.command(name="gallery")
@commands.cooldown(1, 15, commands.BucketType.user)
async def gallery(ctx):
    """Show a gallery of random waifus with prev/next buttons (valid 3 min)."""
    is_nsfw = nsfw_allowed(ctx.channel)
    params = {
        "isNsfw": "True" if is_nsfw else "False",
        "isAnimated": "All",
        "orderBy": "Random",
        "pageSize": 5,
    }
    async with ctx.typing():
        try:
            data = await fetch_images(**params)
        except Exception as e:
            await ctx.reply(f"Could not reach the waifu API: {redact(str(e))}")
            return
    items = data.get("items") or []
    if not items:
        await ctx.reply("No images matched that search.")
        return
    view = GalleryView(items, ctx.author)
    msg = await ctx.send(embed=build_embed(items[0]), view=view)
    view.message = msg
    note_served(items[0])


@bot.command(name="tags")
@commands.cooldown(1, 10, commands.BucketType.user)
async def tags(ctx):
    """List the tags the API knows about, with image counts."""
    try:
        data = await api_get("tags")
    except Exception as e:
        await ctx.reply(f"Could not reach the waifu API: {redact(str(e))}")
        return

    body = "\n".join(
        f"`{t['slug']}` ({t['imageCount']})" for t in data.get("items") or [] if t.get("imageCount")
    )
    await ctx.send(
        embed=discord.Embed(title="Tags", description=body or "none", color=discord.Color.magenta())
    )


@bot.command(name="ping")
async def ping(ctx):
    """Check the bot and the waifu API are both alive."""
    try:
        await api_get("ping")
        status = "up"
    except WaifuApiError as e:
        status = f"HTTP {e.status}"
    except Exception as e:
        status = f"unreachable ({e})"
    await ctx.reply(f"waifu API: {status} | bot latency: {round(bot.latency * 1000)}ms")


@bot.command(name="stats")
async def stats_cmd(ctx):
    """Live numbers: API latency, catalogue size, uptime and error count."""
    embed = discord.Embed(title="Stats", color=discord.Color.magenta())
    embed.add_field(name="API latency", value=f"{STATS['latency_ms']}ms" if STATS["latency_ms"] else "no reading yet")
    embed.add_field(name="Uptime", value=f"{human_duration(time.monotonic() - STARTED_AT)}")
    embed.add_field(name="Served here", value=f"{STATS['served']} images")
    if STATS["errors"]:
        embed.add_field(name="API errors", value=f"{STATS['errors']} since start")
    for key, label in (("images", "Images"), ("tags", "Tags"), ("artists", "Artists")):
        if STATS[key]:
            embed.add_field(name=label, value=f"{STATS[key]:,}")
    embed.set_footer(text=f"Host: {API_BASE}")
    await ctx.reply(embed=embed)


@bot.event
async def on_disconnect():
    pass


# ---------------------------------------------------------------------------
# Slash commands
#
# These are application commands rather than prefix commands for two reasons.
#
# Discord grants an interaction three seconds before it must answer, and an API
# call on a cold Render instance routinely takes longer. Deferring up front
# turns a guaranteed "This interaction failed" into a slow reply. A prefix
# command has no such deadline, which is why the older ones here are still
# prefix commands.
#
# The second reason is discoverability: a slash command is listed with its own
# description, so this set documents itself in the Discord UI where a user
# actually looks.
#
# Every reply that could carry anybody's data is ephemeral. It has to be. A
# shared channel keeps scrollback long after the command is gone, and an
# account listing posted to one is still there tomorrow.
# ---------------------------------------------------------------------------


async def tree_reply(interaction, coro, *, private):
    """Defer, run an API call, and answer with whatever comes back.

    Centralised so every command gets the same three behaviours, each of which
    is easy to forget in one place and impossible to test for: the deferral
    before the first await, the ephemeral flag, and turning an exception into a
    message the caller can read instead of Discord's generic failure notice.
    """
    await interaction.response.defer(ephemeral=private)
    try:
        await coro
    except WaifuApiError as e:
        if interaction.response.is_done():
            await interaction.followup.send(redact(str(e)), ephemeral=True)
        else:
            await interaction.response.send_message(redact(str(e)), ephemeral=True)
    except Exception as e:
        log.error("slash command %s failed: %r", interaction.command.name, e,
                  exc_info=(type(e), e, e.__traceback__))
        message = redact(f"{type(e).__name__}: {e}")
        if interaction.response.is_done():
            await interaction.followup.send(message[:500], ephemeral=True)
        else:
            await interaction.response.send_message(message[:500], ephemeral=True)


async def guarded_tree(interaction):
    """Whether the caller may run a credentialed command, replying if not."""
    allowed, reason = debug_allowed(interaction.user)
    if not allowed:
        await interaction.response.send_message(reason, ephemeral=True)
    return allowed


# --- Public catalogue browsing ---------------------------------------------
#
# No credential is used anywhere in this half, which is what makes it safe to
# leave open to a whole server. Reads go via the CDN host so they answer from
# the edge cache.


@bot.tree.command(name="image", description="Show one image by its id.")
@app_commands.describe(image_id="The numeric image id.")
async def slash_image(interaction: discord.Interaction, image_id: int):
    async def run():
        status, body, _ = await api_request(
            "GET", f"images/{image_id}", base=API_BASE
        )
        if status == 404:
            await interaction.followup.send(f"No image with id `{image_id}`.", ephemeral=True)
            return
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return

        embed = build_embed(body)
        embed.title = f"Image {body.get('id')}"
        embed.add_field(name="Size", value=f"{body.get('width')}x{body.get('height')}", inline=True)
        embed.add_field(name="Format", value=str(body.get("extension")), inline=True)
        tags = ", ".join(t.get("name", "") for t in body.get("tags") or [])
        if tags:
            embed.add_field(name="Tags", value=tags[:1000], inline=False)
        await interaction.followup.send(embed=embed)
        note_served(body)

    await tree_reply(interaction, run(), private=False)


@bot.tree.command(name="tag", description="Five images carrying a given tag.")
@app_commands.describe(slug="Tag slug, e.g. maid.")
async def slash_tag(interaction: discord.Interaction, slug: str):
    async def run():
        status, body, _ = await api_request(
            "GET", "images", base=API_BASE,
            params={"includedTags": slug, "pageSize": 5},
        )
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = (body.get("items") or [])[:5]
        if not items:
            await interaction.followup.send(f"No images tagged `{slug}`.", ephemeral=True)
            return
        view = GalleryView(items, interaction.user)
        msg = await interaction.followup.send(embed=build_embed(items[0]), view=view)
        view.message = msg
        note_served(items[0])

    await tree_reply(interaction, run(), private=False)


@bot.tree.command(name="artist", description="Five images by a given artist.")
@app_commands.describe(name="Artist name.")
async def slash_artist(interaction: discord.Interaction, name: str):
    async def run():
        status, body, _ = await api_request(
            "GET", "images", base=API_BASE,
            params={"includedArtists": name, "pageSize": 5},
        )
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = (body.get("items") or [])[:5]
        if not items:
            await interaction.followup.send(f"No images by `{name}`.", ephemeral=True)
            return
        view = GalleryView(items, interaction.user)
        msg = await interaction.followup.send(embed=build_embed(items[0]), view=view)
        view.message = msg
        note_served(items[0])

    await tree_reply(interaction, run(), private=False)


@bot.tree.command(name="artists", description="Artists in the catalogue, with image counts.")
async def slash_artists(interaction: discord.Interaction):
    async def run():
        status, body, _ = await api_request(
            "GET", "artists", base=API_BASE, params={"pageSize": 25}
        )
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = body.get("items") or []
        lines = "\n".join(
            f"`{a['name']}` ({a.get('imageCount', 0)})" for a in items if a.get("imageCount")
        )
        await interaction.followup.send(
            embed=discord.Embed(
                title="Artists", description=lines or "none", color=discord.Color.magenta()
            )
        )

    await tree_reply(interaction, run(), private=False)


@bot.tree.command(name="version", description="Which build of the API is serving right now.")
async def slash_version(interaction: discord.Interaction):
    async def run():
        status, body, _ = await api_request("GET", "version", base=ORIGIN_BASE)
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        embed = discord.Embed(title="Deployment", color=discord.Color.magenta())
        for key in ("software", "version", "commit", "builtAt"):
            if body.get(key):
                embed.add_field(name=key, value=str(body[key]), inline=False)
        await interaction.followup.send(embed=embed)

    await tree_reply(interaction, run(), private=False)


@bot.tree.command(name="instances", description="Other people's deployments, and whether they work.")
async def slash_instances(interaction: discord.Interaction):
    async def run():
        status, body, _ = await api_request("GET", "instances", base=ORIGIN_BASE)
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = body.get("instances") or []
        if not items:
            await interaction.followup.send("Nobody has registered an instance yet.")
            return
        lines = []
        for entry in items[:20]:
            label = entry.get("name") or entry.get("url") or "?"
            mark = "serving" if entry.get("servingImages") else "no images"
            lines.append(f"[{label}]({entry.get('url')}) — {mark}")
        await interaction.followup.send(
            embed=discord.Embed(
                title=f"Instances ({len(items)})",
                description="\n".join(lines),
                color=discord.Color.magenta(),
            )
        )

    await tree_reply(interaction, run(), private=False)


# --- Credentialed commands -------------------------------------------------
#
# These send the bot's API key, which belongs to an admin account. Two rules
# apply to every one of them, and both exist because this bot runs in servers
# with other people in it:
#
# 1. Allowlisted by Discord account id. The key can delete images, approve
#    review tasks and ban accounts; that is not something a stranger gets.
#
# 2. Ephemeral replies. Even for an allowed caller, the answer is somebody's
#    account data and must not land in a channel.


@bot.tree.command(name="me", description="Which account the bot's API key belongs to.")
async def slash_me(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request("GET", "users/me", auth=True)
        if status >= 400:
            await interaction.followup.send(
                f"HTTP {status}: {format_response(status, body, 'application/json')[:300]}",
                ephemeral=True,
            )
            return
        # Field by field rather than dumping the body: the useful fields are few
        # and anything added later should not be published by default.
        await interaction.followup.send(
            f"Signed in as **{body.get('name')}** "
            f"(id `{body.get('id')}`, role `{body.get('role')}`)",
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="permissions", description="What this account is allowed to do.")
async def slash_permissions(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request("GET", "users/me/permissions", auth=True)
        await interaction.followup.send(
            f"HTTP {status}\n{format_response(status, body, 'application/json')}",
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="albums", description="Albums belonging to the bot's account.")
async def slash_albums(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        me_status, me_body, _ = await api_request("GET", "users/me", auth=True)
        if me_status >= 400:
            await interaction.followup.send(f"HTTP {me_status}", ephemeral=True)
            return
        user_id = (me_body or {}).get("id")

        status, body, _ = await api_request("GET", f"users/{user_id}/albums", auth=True)
        if status >= 400:
            await interaction.followup.send(f"HTTP {status}", ephemeral=True)
            return
        items = body.get("items") or []
        if not items:
            await interaction.followup.send("No albums.", ephemeral=True)
            return
        lines = "\n".join(
            f"`{a.get('name')}` — {a.get('imageCount', 0)} images"
            + (" (default)" if a.get("isDefault") else "")
            for a in items
        )
        await interaction.followup.send(
            embed=discord.Embed(title="Albums", description=lines, color=discord.Color.magenta()),
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="keys", description="API keys on the bot's account.")
async def slash_keys(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request("GET", "auth/api-keys", auth=True)
        if status >= 400:
            await interaction.followup.send(f"HTTP {status}", ephemeral=True)
            return
        keys = body if isinstance(body, list) else (body or {}).get("items") or []
        if not keys:
            await interaction.followup.send("No API keys.", ephemeral=True)
            return
        # The endpoint never returns plaintext — only a hash is stored — but the
        # fields are still chosen rather than dumped.
        lines = "\n".join(
            f"id `{k.get('id')}` — {k.get('description')} "
            f"(created {str(k.get('createdAt'))[:10]}, "
            f"last used {str(k.get('lastUsedAt') or 'never')[:10]})"
            for k in keys
        )
        await interaction.followup.send(
            embed=discord.Embed(title="API keys", description=lines, color=discord.Color.magenta()),
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="review", description="What is waiting in the review queue.")
async def slash_review(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request(
            "GET", "review/tasks", auth=True, params={"pageSize": 10}
        )
        if status >= 400:
            await interaction.followup.send(f"HTTP {status}", ephemeral=True)
            return
        items = body.get("items") or []
        if not items:
            await interaction.followup.send("Review queue is empty.", ephemeral=True)
            return
        lines = "\n".join(
            f"`{t.get('id')}` {t.get('targetType')} {t.get('targetId')} — {t.get('status')}"
            for t in items[:10]
        )
        await interaction.followup.send(
            embed=discord.Embed(
                title=f"Review queue ({body.get('totalCount', len(items))})",
                description=lines,
                color=discord.Color.orange(),
            ),
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="reports", description="Open reports against images.")
async def slash_reports(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request("GET", "reports", auth=True, params={"pageSize": 10})
        if status >= 400:
            await interaction.followup.send(f"HTTP {status}", ephemeral=True)
            return
        items = body.get("items") or []
        if not items:
            await interaction.followup.send("No reports.", ephemeral=True)
            return
        lines = "\n".join(
            f"`{r.get('id')}` image {r.get('imageId')} — {r.get('status')}: "
            f"{str(r.get('description'))[:60]}"
            for r in items[:10]
        )
        await interaction.followup.send(
            embed=discord.Embed(
                title=f"Reports ({body.get('totalCount', len(items))})",
                description=lines,
                color=discord.Color.orange(),
            ),
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="adminstats", description="The operator dashboard numbers.")
async def slash_adminstats(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request("GET", "stats/admin", auth=True)
        await interaction.followup.send(
            f"HTTP {status}\n{format_response(status, body, 'application/json')}",
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)


@bot.tree.command(name="diff", description="Rows with no matching object in storage.")
async def slash_diff(interaction: discord.Interaction):
    if not await guarded_tree(interaction):
        return

    async def run():
        status, body, _ = await api_request(
            "GET", "storage-diff", auth=True,
            params={"mode": "MissingFromS3", "pageSize": 5},
        )
        if status >= 400:
            await interaction.followup.send(
                f"HTTP {status}: {format_response(status, body, 'application/json')[:300]}",
                ephemeral=True,
            )
            return
        items = body.get("items") or []
        lines = "\n".join(f"image `{i.get('id')}`" for i in items[:5])
        await interaction.followup.send(
            f"Rows with no object in storage: **{body.get('totalCount', len(items))}**\n"
            f"{lines or 'none'}",
            ephemeral=True,
        )

    await tree_reply(interaction, run(), private=True)




# Cooldowns are the expected case, not a fault.
#
# discord.py raises CommandOnCooldown and, with no handler, the default
# on_command_error logs the full traceback. Three commands carry a cooldown and
# the cooldown is 10-15 seconds, so a user double-tapping !waifu produced a
# multi-line stack trace in the console for behaviour that is working exactly as
# intended. The user got silence; the operator got noise, inverted from what
# either of them wanted.
#
# Only genuine faults still get logged with a traceback, which is the point:
# an error handler that swallows everything would hide the next real bug.
@bot.event
async def on_command_error(ctx, error):
    if isinstance(error, commands.CommandOnCooldown):
        # Rounded up, deliberately. human_duration truncates, so a real wait of
        # 14.6s would be reported as "14s" and the user would retry half a
        # second early and be cooled down again - the message would be causing
        # the very thing it is warning about. Ceiling also turns a sub-second
        # remainder such as 0.13s into 1s rather than the "0s" truncation
        # produces, which would tell them the wait is over while it is not.
        wait = human_duration(math.ceil(error.retry_after))
        await ctx.reply(f"slow down — try again in {wait}" if wait != "0s"
                        else "slow down — one moment")
        return

    # A mistyped prefix is nobody's fault and needs no reply, only no traceback.
    if isinstance(error, commands.CommandNotFound):
        return

    if isinstance(error, commands.MissingPermissions):
        await ctx.reply("you don't have permission to use that here.")
        return

    if isinstance(error, commands.BotMissingPermissions):
        await ctx.reply("i'm missing permissions to do that.")
        return

    log.error("command %s failed: %r", getattr(ctx.command, "name", "?"), error,
              exc_info=(type(error), error, error.__traceback__))


@bot.tree.command(
    name="register",
    description="List your own waifu API instance on the public index.",
)
@app_commands.describe(
    url="The base URL of your instance, for example https://waifu.example",
    name="A display name for the entry (optional)",
)
async def register_instance(
    interaction: discord.Interaction,
    url: str,
    name: str | None = None,
):
    """Register an instance.

    The backend probes it before storing anything: an instance that cannot
    answer its own handshake, return a readable catalogue, or serve an image is
    refused rather than listed as working.

    The probe makes three outbound requests, which exceeds Discord's three-second
    acknowledgement window, so the reply is deferred first and sent ephemerally -
    the result is about the caller's own instance and there is no reason for
    anyone else to see it.
    """
    if not ADMIN_API_KEY:
        await interaction.response.send_message(
            "This bot is not configured to register instances. Set WAIFU_ADMIN_API_KEY "
            "on it and try again.",
            ephemeral=True,
        )
        return

    await interaction.response.defer(ephemeral=True)

    # The lock covers the cooldown check, the request and the write, so two
    # people running this in the same second cannot both see an empty cooldown.
    async with _register_lock:
        now = datetime.now(timezone.utc)
        log_data = load_register_log()

        wait = register_cooldown_remaining(log_data, now)
        if wait > 0:
            record_attempt(log_data, interaction, url, name, "rate_limited", None)
            save_register_log(log_data)
            await interaction.followup.send(
                f"One registration an hour, across the whole server. "
                f"Try again in {describe_wait(wait)}.",
                ephemeral=True,
            )
            return

        try:
            status, stored = await api_post_instances(
                url, name=name, discord_user_id=str(interaction.user.id)
            )
        except WaifuApiError as error:
            detail = error.detail or "The API rejected the registration."
            if error.status == 400:
                # Rejected before anything is fetched, so there is nothing to
                # rate limit and no reason to make the caller wait.
                record_attempt(log_data, interaction, url, name, "invalid", detail)
                save_register_log(log_data)
            elif error.status == 422:
                detail = f"Your instance did not answer correctly.\n{detail}"
                record_attempt(log_data, interaction, url, name, "probe_failed", detail)
                log_data["lastCooldownAt"] = now.isoformat()
                save_register_log(log_data)
            else:
                if error.status == 403:
                    detail = "This bot is not allowed to register instances."
                record_attempt(log_data, interaction, url, name, "refused", detail)
                log_data["lastCooldownAt"] = now.isoformat()
                save_register_log(log_data)

            await interaction.followup.send(redact(detail), ephemeral=True)
            return
        except Exception:
            log.error("%s", redact(traceback.format_exc()))
            # An unreachable API is not the caller's fault, so no cooldown: they
            # should be able to retry the moment it comes back.
            record_attempt(log_data, interaction, url, name, "api_unreachable", None)
            save_register_log(log_data)
            await interaction.followup.send(
                "Could not reach the API. Nothing was changed - try again in a moment.",
                ephemeral=True,
            )
            return

        # Success, or already registered. Both start the clock: one registration
        # an hour is what was asked for, and re-running the command is not a way
        # to get a second one.
        already = status == 200
        outcome = "already_registered" if already else "registered"
        record_attempt(log_data, interaction, url, name, outcome, None)
        log_data["lastCooldownAt"] = now.isoformat()
        save_register_log(log_data)
        message = (
            f"**{stored.get('name') or stored.get('url')}** is already on the list; "
            "nothing was changed."
            if already
            else f"Registered **{stored.get('name') or stored.get('url')}**.\n"
                 f"{stored.get('url')} is now on the public list."
        )

    if OPERATOR_ROLE_ID and not already:
        member = interaction.user
        if member.guild is None:
            message += "\n\nYou used this in DMs, so the operator role was not granted."
        else:
            role = member.guild.get_role(int(OPERATOR_ROLE_ID))
            if role is None:
                message += "\n\nThe configured operator role no longer exists."
            elif role in member.roles:
                message += "\n\nYou already have the operator role."
            else:
                try:
                    await member.add_roles(role)
                    message += "\n\nYou have been given the operator role."
                except discord.Forbidden:
                    message += (
                        "\n\nI could not grant the operator role: I need the Manage Roles "
                        "permission and my highest role has to be above it."
                    )

    await interaction.followup.send(message, ephemeral=True)


@bot.event
async def on_ready():
    global session
    session = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30))
    log.info("logged in as %s", bot.user)

    # Said at startup rather than on first use: the failure mode is a 401 with a
    # message about the API rejecting the registration, which sends whoever is
    # trying it looking in entirely the wrong place.
    if ADMIN_API_KEY and register_target_is_cdn(REGISTER_TARGET):
        log.warning(
            "/register is configured but would post to the CDN (%s), which drops "
            "X-Api-Key. Set WAIFU_ORIGIN to the API host in .env or every "
            "registration will be rejected.",
            REGISTER_TARGET,
        )

    # Slash commands are registered globally rather than per guild, so the
    # command appears immediately everywhere instead of after Discord's
    # per-guild propagation.
    try:
        synced = await bot.tree.sync()
        log.info("synced %d slash commands", len(synced))
    except Exception:
        log.error("%s", redact(traceback.format_exc()))

    bot.loop.create_task(update_status())
    bot.loop.create_task(probe_api())


# What the bot was last asked to do, and when.
#
# Recorded from a single on_command_completion hook rather than inside each
# command, so a new command is picked up for free.
_last_activity = {"command": None, "at": 0.0}

# How long a command counts as "recent" for the reactive status slot.
ACTIVITY_MEMORY_SECONDS = 30 * 60

# The rotating status lines. Activity types are varied on purpose: four
# consecutive identical types read as a stuck bot.
#
# A None entry is a live slot: it reports a number from STATS and falls back to
# a fixed line until there is a number to report. Slots holding a tuple are
# always that line, whatever the API is doing.
STATUS_ROTATION = [
    None,  # API latency
    (discord.ActivityType.playing, "routing tags since 2021"),
    None,  # catalogue size
    None,  # reactive: the last command used
    (discord.ActivityType.playing, "hunting duplicates at Hamming <= 4"),
    None,  # uptime and errors
    (discord.ActivityType.competing, "PNG vs WebP, and the winner is WebP"),
    (discord.ActivityType.listening, "C should have gone in the tags"),
    None,  # most recently served image
    (discord.ActivityType.watching, "signed GETs, one at a time"),
    (discord.ActivityType.competing, "edge cache vs Render cold start"),
    (discord.ActivityType.listening, "png in, webp out, nobody notices"),
    (discord.ActivityType.playing, "a tag is a promise"),
    (discord.ActivityType.competing, "case-sensitive vs case-insensitive"),
    (discord.ActivityType.watching, "SIGV4, hand-rolled, no SDK"),
    (discord.ActivityType.listening, "the render farm that never was"),
    (discord.ActivityType.playing, "Hamming distance, going by feel"),
    (discord.ActivityType.watching, "waiting out a 60 second cache TTL"),
    (discord.ActivityType.competing, "ObjectKeyCandidates vs {id}{ext}"),
    (discord.ActivityType.listening, "pgvector, doing its honest best"),
]

# Spare lines, used when a live slot has nothing to say yet.
#
# Kept disjoint from STATUS_ROTATION, or the same line can appear two slots
# apart and read as a stuck bot.
FALLBACK_STATUSES = [
    (discord.ActivityType.playing, "resizing things that were already the right size"),
    (discord.ActivityType.listening, "the sound of a cold start"),
    (discord.ActivityType.competing, "your browser's connection pool"),
    (discord.ActivityType.watching, "the Cloudflare edge, probably"),
    (discord.ActivityType.competing, "the upload queue, politely"),
    (discord.ActivityType.listening, "nobody is watching this, only me"),
    (discord.ActivityType.playing, "counting tags, badly"),
]

# Status lines that react to the last command used. This is what makes the
# presence feel awake rather than ticking along on a timer.
ACTIVITY_STATUSES = {
    "waifu": (discord.ActivityType.watching, "one waifu at a time"),
    "gallery": (discord.ActivityType.watching, "five images, three minutes, no pressure"),
    "tags": (discord.ActivityType.competing, "the entire tag list, top to bottom"),
    "ping": (discord.ActivityType.listening, "waiting on the API, as usual"),
    "stats": (discord.ActivityType.watching, "its own homework"),
}

# How often to re-measure, for the lines that should not go stale while nobody
# is talking to the bot.
#
# The edge caches anonymous reads for 60s, so a 60s probe costs one origin
# request a minute at worst. /stats/public barely changes, so it is read far
# less often than it is asked for.
PROBE_SECONDS = 60
COUNT_REFRESH_SECONDS = 30 * 60

# A failure this recent is worth reporting even if later calls have succeeded;
# older than this it is history, and the uptime line takes over.
ERROR_WINDOW_SECONDS = 15 * 60


@bot.event
async def on_command_completion(ctx):
    _last_activity["command"] = ctx.command.name
    _last_activity["at"] = time.monotonic()


def human_duration(seconds):
    """A duration as one short unit: 45s, 12m, 6h, 3d. Discord limits a status
    to 128 characters, so there is no room for '3 days, 4 hours'."""
    seconds = int(seconds)
    if seconds < 60:
        return f"{seconds}s"
    if seconds < 3600:
        return f"{seconds // 60}m"
    if seconds < 86400:
        return f"{seconds // 3600}h"
    return f"{seconds // 86400}d"


def latency_status():
    if STATS["latency_ms"] is None:
        return None
    ms = STATS["latency_ms"]
    if ms < 100:
        return (discord.ActivityType.watching, f"the API, {ms}ms away")
    if ms < 500:
        return (discord.ActivityType.listening, f"the API, {ms}ms away and thinking")
    return (discord.ActivityType.playing, f"the API, {ms}ms away — Render's coffee is cold")


def catalogue_status():
    """Cycle through whichever totals /stats/public has given us.

    One number is a fact; four are a pitch. They arrive together and mean the
    same thing, so picking among them keeps a repeated line from reading as a
    stuck bot.
    """
    available = [
        (STATS["images"], "{:,} waifus and still counting"),
        (STATS["tags"], "{:,} tags, all of them load-bearing"),
        (STATS["artists"], "{:,} artists, quietly going uncredited"),
    ]
    available = [(value, template) for value, template in available if value]
    if not available:
        return None
    value, template = random.choice(available)
    return discord.ActivityType.watching, template.format(value)


def service_status():
    """Uptime and errors. The error line wins while it is fresh, because 'is it
    broken right now' is the question this line exists to answer."""
    since_error = (
        None if STATS["last_error_at"] is None
        else time.monotonic() - STATS["last_error_at"]
    )
    if since_error is not None and since_error < ERROR_WINDOW_SECONDS:
        ago = human_duration(since_error)
        if STATS["latency_ms"] is not None:
            return (discord.ActivityType.watching, f"last error {ago} ago, still serving")
        return (discord.ActivityType.playing, f"last error {ago} ago, still trying")

    uptime = human_duration(time.monotonic() - STARTED_AT)
    served = STATS["served"]
    if served:
        return (discord.ActivityType.playing, f"up {uptime}, {served} served")
    return (discord.ActivityType.listening, f"up {uptime}, waiting to be useful")


def last_served_status():
    """The most recent image handed out, and when.

    The only slot that moves on its own rather than on a timer, which is what
    makes the presence look awake between commands. Freshness is reported
    because a stale "latest #4028" with no timestamp reads as a live one, and a
    bot claiming to have just served something when it served it four hours ago
    is worse than saying nothing.
    """
    last_id = STATS["last_id"]
    served_at = STATS["last_served_at"]
    if last_id is None or served_at is None:
        return None

    served = STATS["served"]
    ago = human_duration(time.monotonic() - served_at)
    if ago != "0s":
        return (discord.ActivityType.listening, f"last served #{last_id}, {ago} ago")

    if served == 1:
        return (discord.ActivityType.playing, "one image down and counting")
    return (discord.ActivityType.playing, f"{served:,} served, newest #{last_id}")


LIVE_STATUSES = {
    0: latency_status,
    2: catalogue_status,
    5: service_status,
    8: last_served_status,
}


def current_status(slot):
    """Return the (type, name) to show for this slot of the rotation.

    Slots 0, 2, 5 and 8 read numbers from STATS; slot 3 reacts to the last
    command; the rest are fixed lines. A live slot with nothing to report falls
    back to a spare line, so the status never depends on the API being up or on
    someone having typed a command.

    Slot numbers are load-bearing - they are the keys of LIVE_STATUSES - so new
    live slots are appended rather than inserted, and anything that renumbers
    the list silently reassigns which function feeds which line.
    """
    slot %= len(STATUS_ROTATION)

    live = LIVE_STATUSES.get(slot)
    if live is not None:
        return live() or random.choice(FALLBACK_STATUSES)

    entry = STATUS_ROTATION[slot]
    if entry is not None:
        return entry

    command = _last_activity["command"]
    recent = time.monotonic() - _last_activity["at"] < ACTIVITY_MEMORY_SECONDS
    if recent and command in ACTIVITY_STATUSES:
        return ACTIVITY_STATUSES[command]
    return random.choice(FALLBACK_STATUSES)


async def update_status():
    await bot.wait_until_ready()
    slot = 0
    while not bot.is_closed():
        try:
            activity_type, name = current_status(slot)
            await bot.change_presence(
                activity=discord.Activity(name=name, type=activity_type)
            )
        except Exception as e:
            log.warning("status update failed: %s", redact(str(e)))

        slot += 1

        # Jitter the interval so the presence does not read as a cron job, and
        # stay well clear of Discord's limit of roughly two presence changes per
        # five minutes: the floor here is 480s.
        await asyncio.sleep(random.randint(480, 780))


async def probe_api():
    """Keep the live status lines current without waiting for someone to run a
    command, which is the whole reason they are worth showing.

    Two rates: /ping is cheap and tells us latency and whether the API is
    answering at all, /stats/public is a bigger payload and barely moves, so it
    is read far less often.

    The ping goes to ORIGIN_BASE, not API_BASE, and carries the longer timeout.
    Through the edge it would be served from a 60s cache on a 60s interval, so
    it would only reach Render about half the time and would not reliably keep
    the instance awake; and when the instance had spun down, the 30s default
    would expire before a cold start finished and record a working API as down.
    """
    await bot.wait_until_ready()
    last_counts = 0.0
    while not bot.is_closed():
        try:
            await api_get("ping", base=ORIGIN_BASE, timeout=PROBE_TIMEOUT_SECONDS)
            if time.monotonic() - last_counts >= COUNT_REFRESH_SECONDS:
                # A failed refresh is already counted by api_get; the next round
                # retries, so there is nothing extra to record here.
                counts = await api_get("stats/public")
                STATS["images"] = counts.get("totalImages")
                STATS["tags"] = counts.get("totalTags")
                STATS["artists"] = counts.get("totalArtists")
                last_counts = time.monotonic()
        except Exception as e:
            log.warning("probe failed: %s", redact(str(e)))
        await asyncio.sleep(PROBE_SECONDS)


bot.run(os.environ["DISCORD_TOKEN"], log_handler=None)
