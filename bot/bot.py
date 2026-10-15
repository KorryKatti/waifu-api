import os
import logging
import math
import asyncio
import random
import time
import urllib.parse
import traceback

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


def redact(text: str) -> str:
    """Strip the admin key from any string on its way to a log or a message.

    No code path here puts the key into a third-party string: the request log
    line carries method, path, status and duration and never headers, and the key
    is sent as a request header. So today this is defence in depth rather than a
    fix for an observed leak.

    It stays because the cost of being wrong is a permanent, non-expiring write
    credential in somebody's log file, and because an HTTP library putting request
    headers into an exception message is exactly the kind of thing that changes
    without warning in a patch release. Scrubbing at the boundary means that
    possibility costs nothing.
    """
    if ADMIN_API_KEY and ADMIN_API_KEY in text:
        return text.replace(ADMIN_API_KEY, "[redacted]")
    return text

# The role granted to somebody whose instance is on the index. Empty means no
# grant, and the command says so rather than appearing to have worked.
OPERATOR_ROLE_ID = os.getenv("INSTANCE_OPERATOR_ROLE", "").strip()

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

    def __init__(self, message, status=None):
        super().__init__(message)
        self.status = status


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
    endpoint = f"{(ORIGIN_BASE or API_BASE)}/instances"

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
            return body
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

    try:
        stored = await api_post_instances(
            url, name=name, discord_user_id=str(interaction.user.id)
        )
    except WaifuApiError as e:
        detail = e.detail or "The API rejected the registration."
        if e.status == 403:
            detail = "This bot is not allowed to register instances."
        elif e.status == 422:
            detail = f"Your instance did not answer correctly.\n{detail}"
        await interaction.followup.send(redact(detail), ephemeral=True)
        return
    except Exception:
        log.error("%s", redact(traceback.format_exc()))
        await interaction.followup.send(
            "Could not reach the API. Nothing was changed.", ephemeral=True
        )
        return

    message = (
        f"Registered **{stored.get('name') or stored.get('url')}**.\n"
        f"{stored.get('url')} is now on the public list."
    )
    if OPERATOR_ROLE_ID:
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
