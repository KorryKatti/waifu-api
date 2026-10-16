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

# The API host itself, rather than the Worker in front of it.
#
# Used where a request has to reach Render: /register, which authenticates and
# which the Worker would strip the credential from, and /version and /instances,
# which want the origin's own answer rather than a cached one.
#
# It used to be the keep-alive probe's target too, and that was the whole reason
# it existed - the edge caches /ping for 60s, so probing through the Worker only
# reached Render about half the time and a 60-second probe against a 60-second
# cache TTL is a coin flip decided by sub-second jitter. Something else keeps
# Render awake now, so nothing here polls the origin on a timer.
ORIGIN_BASE = os.getenv("WAIFU_ORIGIN", API_BASE).rstrip("/")

PREFIX = os.getenv("PREFIX", "!")

# Ordinary API calls give up after 30s. Reads that may meet a sleeping origin
# override this: a free-tier instance spins down after about fifteen idle
# minutes, and nothing here holds it awake any more, so a cold start is the
# normal case rather than the exception. Timing out mid-wake scores a working API
# as an error.
DEFAULT_TIMEOUT_SECONDS = 30
ORIGIN_TIMEOUT_SECONDS = 90

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

# Where /instances needed:true records who asked and when.
#
# A separate file from the registration log rather than a second key in that one,
# because the two limit different things and a corrupt file should not silently
# disable both. Gitignored, contains Discord ids, stays local.
INSTANCE_PING_LOG_PATH = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "instance_ping_log.json"
)

# Serialises the whole of a forced measurement - cooldown check, the requests,
# and the write.
#
# Not just for the file. Two people passing needed:true in the same second would
# otherwise both pass the cooldown, both fan out to every instance at once, and
# both write; the second write would also land a timestamp the first had already
# superseded, which is how a limiter ends up reporting a shorter gap than the one
# that actually happened.
_instance_ping_lock = asyncio.Lock()


def load_json_log(path: str, label: str, cooldown_key: str) -> dict:
    """Read a command's log, treating anything unreadable as empty.

    A corrupt file must not brick the command. The worst outcome of starting
    empty is one uncharged run, which is also what happens the first time the
    command is ever used.

    Shared by every command that keeps a local cooldown file, because the part
    that matters is not the bookkeeping - it is that a half-written or truncated
    file reads back as a fresh limiter rather than as a crash.
    """
    empty = {cooldown_key: None, "entries": []}
    try:
        with open(path, encoding="utf-8") as handle:
            data = json.load(handle)
    except FileNotFoundError:
        return empty
    except (OSError, ValueError) as error:
        log.warning("%s log unreadable, starting a new one: %s", label, error)
        return empty

    if not isinstance(data, dict):
        return empty
    data.setdefault(cooldown_key, None)
    data.setdefault("entries", [])
    return data


def save_json_log(path: str, label: str, data: dict) -> None:
    """Write a command's log atomically.

    Failures are logged and swallowed. Losing the history is annoying; refusing
    to run a command because a file could not be written is worse.
    """
    data["entries"] = data["entries"][-REGISTER_LOG_LIMIT:]

    temporary = f"{path}.tmp"
    try:
        with open(temporary, "w", encoding="utf-8") as handle:
            json.dump(data, handle, indent=2)
        os.replace(temporary, path)
    except OSError as error:
        log.warning("could not write the %s log: %s", label, error)
        try:
            os.unlink(temporary)
        except OSError:
            pass


def load_register_log() -> dict:
    return load_json_log(REGISTER_LOG_PATH, "register", "lastCooldownAt")


def save_register_log(data: dict) -> None:
    save_json_log(REGISTER_LOG_PATH, "register", data)


def cooldown_remaining(data: dict, now: datetime, window: float) -> float:
    """Seconds until the next run is allowed, or zero."""
    raw = data.get("lastCooldownAt")
    if not raw:
        return 0.0
    try:
        last = datetime.fromisoformat(raw)
    except (TypeError, ValueError):
        return 0.0

    return max(0.0, window - (now - last).total_seconds())


def register_cooldown_remaining(data: dict, now: datetime) -> float:
    return cooldown_remaining(data, now, REGISTER_COOLDOWN_SECONDS)


def instance_ping_cooldown_remaining(data: dict, now: datetime) -> float:
    return cooldown_remaining(data, now, INSTANCE_PING_COOLDOWN_SECONDS)


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


# --- Instance latency ------------------------------------------------------
#
# /instances reports whether each deployment works and says nothing about how
# fast it is, which is the question somebody actually has when deciding where to
# point a client.
#
# The uptime on that list comes from the backend's prober, running on Render.
# These measurements come from wherever this bot runs, which is usually another
# country and a residential connection. That is the entire reason to do it here
# rather than read a number the backend could have published: it is a second
# vantage point, not a better one.
#
# It is also not the reader's latency, and the output says so. A millisecond
# figure that does not name the line it was measured on is a confident wrong
# answer for anyone reading it from another continent.

# How often to re-measure on its own.
#
# Hourly, and not on a shorter interval for a reason that is not throughput: a
# request to a listed instance is a request to somebody else's free-tier
# deployment, and a sleeping one has to start up to answer it. A sweep every
# fifteen minutes kept those instances marginally warm all day for a number that
# barely moves - inter-region routing drifts over hours, not minutes. Once an hour
# is the point where the figure would have changed anyway.
#
# Somebody who wants it now asks, with `needed: true`, which is limited to one
# per hour across the server so this stays the floor rather than becoming the
# ceiling.
#
# 0 switches the measurements off, and /instances then reports the index alone.
INSTANCE_PING_SECONDS = int(os.getenv("INSTANCE_PING_SECONDS", "3600"))

# Gap between forced measurements, whole server.
#
# Same reasoning as REGISTER_COOLDOWN_SECONDS and for the same reason: the thing
# being limited is not the person but this bot's connection and other people's
# servers. Per-user limits would let a channel-full of members each trigger a
# sweep in the same minute.
#
# Matching the sweep interval is deliberate. A forced measurement is worth
# exactly as much as a scheduled one, so allowing more of them than the schedule
# already provides would make the schedule pointless.
INSTANCE_PING_COOLDOWN_SECONDS = int(os.getenv("INSTANCE_PING_COOLDOWN_SECONDS", "3600"))

# Deadline for one measurement. Short on purpose: a Render free-tier instance
# that has spun down is not going to answer inside a cold start, so holding the
# sweep open for one would make every sweep as slow as the worst entry on the
# list. A timeout here reads as "no answer", which is true, and is what the
# backend's own prober would record for the same instance.
INSTANCE_PING_TIMEOUT_SECONDS = 3.0

# How many instances one sweep will dial. The index is small in practice; the cap
# is so that a list which grew unexpectedly could not become an unbounded fan-out
# from the operator's connection.
INSTANCE_PING_LIMIT = 25

# Redirect hops allowed while measuring. Enough for the ordinary http -> https
# move, and bounded because the destination is a URL on a public list rather than
# a server this bot administers.
INSTANCE_PING_MAX_REDIRECTS = 3

# How old a measurement may be before the command says so. Two sweeps, so one
# skipped or failed round does not immediately present every figure as stale.
#
# Floored at a minute because the arithmetic alone degenerates: an interval of 0
# switches the measurements off, and 2 * 0 would mark a just-taken figure stale
# before anybody could read it.
INSTANCE_PING_FRESH_SECONDS = max(2 * INSTANCE_PING_SECONDS, 60)

# Measured round trip per instance, keyed by the URL exactly as the index spells
# it, because that is the key the command has to hand.
#
# url -> {"ms": float | None, "status": int | None, "error": str | None,
#         "at": float}
#
# `at` is monotonic, like STARTED_AT and last_served_at, so the age survives a
# wall-clock change.
#
# Replaced wholesale by each successful sweep rather than merged into, so a URL
# that has left the index stops being reported. A failed sweep leaves it
# untouched: not reaching the index is a reason to say the numbers are old, not a
# reason to throw them away.
INSTANCE_PINGS: dict[str, dict] = {}


def instance_probe_url(base_url: str) -> str | None:
    """The URL to measure, or None when this entry is not something to dial.

    Scheme, host and no embedded credentials. The address block list the backend
    enforces is deliberately not repeated here: it exists to stop the *backend*,
    which sits in a datacentre beside a database, from being talked into dialling
    an internal address on request. This bot dials a list only an admin can write
    to, from the operator's own machine, where an internal address is an address
    the operator owns.

    What is still worth refusing is a URL this bot has no business fetching: a
    non-HTTP scheme, or one carrying credentials that would otherwise reach a log
    line and an outbound header.
    """
    try:
        parsed = urllib.parse.urlparse(base_url)
    except ValueError:
        return None
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        return None
    if parsed.username or parsed.password:
        return None
    return urllib.parse.urlunparse(
        (parsed.scheme, parsed.netloc, parsed.path.rstrip("/"), "", "", "")
    )


def describe_age(seconds: float) -> str:
    """An age short enough to read at a glance."""
    minutes = int(seconds // 60)
    if minutes < 1:
        return "under a minute"
    if minutes < 60:
        return f"{minutes} min"
    hours = minutes // 60
    return f"{hours} h" if hours < 48 else f"{hours // 24} d"


def describe_ping(url: str, now: float) -> str:
    """The latency suffix for one line of /instances.

    Unmeasured, unreachable and slow are three different claims and are rendered
    as three different things. Collapsing them is how a list comes to look
    healthy because nobody measured it.
    """
    record = INSTANCE_PINGS.get(url)
    if record is None or (record.get("ms") is None and not record.get("error")):
        return "not measured yet"

    if record.get("error"):
        text = {
            "timeout": f"no answer in {int(INSTANCE_PING_TIMEOUT_SECONDS)}s",
            "sweep": "measurement failed",
        }.get(record["error"], "no answer")
    else:
        text = f"{round(record['ms'])} ms"
        status = record.get("status")
        # A fast 404 is a fast answer to a request that should not have 404'd.
        # On its own, rounded, it reports a broken handshake as a quick one.
        if status and not 200 <= status < 300:
            text += f" (HTTP {status})"

    age = now - record.get("at", now)
    if age > INSTANCE_PING_FRESH_SECONDS:
        text += f", {describe_age(age)} old"
    return text


async def measure_instance(sess, url: str) -> dict:
    """Time one GET /version against an instance.

    /version because the index documents it as answering with no database and no
    credentials, which is the only endpoint on a stranger's deployment a bot can
    ask for. The image URL is deliberately not touched: that check belongs to the
    backend's prober, and repeating it would spend the operator's CDN on a
    measurement about their latency.

    A response of any status is a measurement, because what is being timed is the
    round trip to the host and a fast 404 is still a fast host. The status is
    carried rather than dropped so a 142 ms handshake stays distinguishable from a
    142 ms 404 once both are rounded for display.
    """
    started = time.monotonic()
    # The handshake, not the root. A deployment with a base path answers
    # /api/version rather than /version, and appending to the URL as the index
    # spells it gets that right without this function needing to know about base
    # paths. instance_probe_url has already stripped any trailing slash, so this
    # cannot double up.
    try:
        async with sess.get(
            f"{url}/version", max_redirects=INSTANCE_PING_MAX_REDIRECTS
        ) as resp:
            await resp.read()
            return {
                "ms": (time.monotonic() - started) * 1000,
                "status": resp.status,
                "error": None,
            }
    except (asyncio.TimeoutError, TimeoutError):
        return {"ms": None, "status": None, "error": "timeout"}
    except (aiohttp.ClientError, OSError) as error:
        # Network-level failures only, and that restriction is the point. A
        # NameError from a typo in this function must not come back as a recorded
        # "no answer": that is a broken measurement wearing the costume of a
        # working one, and an instance index that reports its own bugs as other
        # people's outages is worse than one that admits it is unsure. Anything
        # else is a bug, and reaches the sweep's gather, which logs it.
        return {"ms": None, "status": None, "error": f"{type(error).__name__}: {error}"}


async def fetch_index(sess) -> tuple:
    """GET /instances, returning (status, body), recording nothing.

    Deliberately not api_request. That writes into STATS["latency_ms"] on
    success, which is the /ping round trip shown in the status line, and a sweep
    running every fifteen minutes would spend most of its life overwriting that
    number with the time taken to fetch an index nobody is waiting for.

    The 30s here rather than the 3s the measurements get: this is our own API,
    answering one small query, and a timeout against it should mean something
    actually went wrong rather than being swept up by a slow instance elsewhere
    in the same session.
    """
    timeout = aiohttp.ClientTimeout(total=DEFAULT_TIMEOUT_SECONDS)
    async with sess.get(
        f"{ORIGIN_BASE}/instances",
        timeout=timeout,
        max_redirects=INSTANCE_PING_MAX_REDIRECTS,
    ) as resp:
        if resp.content_type.endswith("json"):
            return resp.status, await resp.json(content_type=None)
        return resp.status, await resp.text()


def plan_instance_pings(entries) -> dict:
    """Index entries -> the URLs to dial, dropping anything not worth dialling.

    Keyed by the index's own spelling of the URL, because that is the key the
    command has to hand; the value is the normalised form to actually request.
    """
    plan = {}
    for entry in entries or []:
        if not isinstance(entry, dict):
            continue
        raw = str(entry.get("url") or "")
        dialable = instance_probe_url(raw)
        if dialable:
            plan[raw] = dialable
    return plan


async def measure_instance_urls(plan: dict) -> int:
    """Measure every URL in a plan concurrently, updating the cache in place.

    Returns how many were measured.

    Concurrent because the sweep should cost about as long as the slowest single
    instance rather than the sum of them. That is what keeps one sleeping entry
    from setting the pace for everything else, and it is also what makes a forced
    measurement viable at all: serially, twenty-five instances at three seconds
    each is a seventy-five second wait on somebody's command.
    """
    if not plan:
        INSTANCE_PINGS.clear()
        return 0

    urls = list(plan)[:INSTANCE_PING_LIMIT]
    if len(plan) > len(urls):
        log.info("instance sweep: measuring %d of %d entries", len(urls), len(plan))

    connector = aiohttp.TCPConnector(force_close=True, limit=INSTANCE_PING_LIMIT)
    timeout = aiohttp.ClientTimeout(total=INSTANCE_PING_TIMEOUT_SECONDS)
    async with aiohttp.ClientSession(timeout=timeout, connector=connector) as sess:
        results = await asyncio.gather(
            *(measure_instance(sess, plan[url]) for url in urls),
            return_exceptions=True,
        )

    at = time.monotonic()
    measured = {}
    for url, result in zip(urls, results):
        if isinstance(result, asyncio.CancelledError):
            # The bot is shutting down. Not a measurement failure and not
            # something to record, so it goes no further than here.
            raise result
        if isinstance(result, BaseException):
            # A bug in measure_instance rather than anything about the instance.
            # Recorded under its own code so the command says "measurement
            # failed" instead of blaming somebody else's server for it.
            log.warning("instance ping %s raised: %r", redact(url), result)
            result = {"ms": None, "status": None, "error": "sweep"}
        measured[url] = {**result, "at": at}

    # clear() then update() rather than rebinding the name. Still a wholesale
    # replacement, so a URL that has left the index stops being reported - but a
    # module-level cache that can be swapped out from under any code holding a
    # reference to it is a stale-snapshot bug waiting for whoever writes the next
    # thing that reads it.
    INSTANCE_PINGS.clear()
    INSTANCE_PINGS.update(measured)
    for url in INSTANCE_PINGS:
        log.info("instance ping: %s -> %s", redact(url), describe_ping(url, at))
    return len(measured)


async def refresh_instance_pings() -> int:
    """The scheduled sweep: read the index, then measure everything on it.

    Returns how many were measured, or zero if the index could not be read.

    A failure to read the index keeps the last measurements rather than clearing
    them. Not reaching our own API is a reason to say the figures are old, not a
    reason to throw them away - and the command labels their age, so it can say
    so honestly rather than showing a column of nothing.
    """
    connector = aiohttp.TCPConnector(force_close=True, limit=INSTANCE_PING_LIMIT)
    timeout = aiohttp.ClientTimeout(total=INSTANCE_PING_TIMEOUT_SECONDS)
    async with aiohttp.ClientSession(timeout=timeout, connector=connector) as sess:
        try:
            status, body = await fetch_index(sess)
        except Exception as error:
            log.warning(
                "instance sweep: could not read the index, keeping the last "
                "measurements: %s",
                redact(str(error)),
            )
            return 0
        if status >= 400 or not isinstance(body, dict):
            log.warning(
                "instance sweep: GET /instances = %s, keeping the last measurements",
                status,
            )
            return 0
        entries = body.get("instances") or []

    if not entries:
        log.info("instance sweep: the index is empty, nothing to measure")
        INSTANCE_PINGS.clear()
        return 0
    return await measure_instance_urls(plan_instance_pings(entries))


async def force_instance_measurement(interaction, items) -> tuple:
    """Measure now, if the server has not already done so this hour.

    Returns (refusal, note). A refusal is a message to send instead of the list
    and the caller stops; a note is a line to put above the list it goes on to
    render. Exactly one of the two is ever set.

    This is the one place where measuring happens inside a command, which is why
    it needs the limit: /instances is public, so an unlimited version would let
    anybody in the server choose when this bot fans out to every listed
    deployment. Once an hour, whole server, is the same bargain /register makes
    for the same reason.

    The command has already deferred, so the wait is visible as a slow reply
    rather than a failed interaction. The measurements run concurrently and each
    gives up after INSTANCE_PING_TIMEOUT_SECONDS, so the worst case is that
    timeout plus the index read the command has already done - which is also why
    the entries the command fetched are reused rather than fetched again.
    """
    async with _instance_ping_lock:
        now = datetime.now(timezone.utc)
        data = load_json_log(INSTANCE_PING_LOG_PATH, "instance ping", "lastCooldownAt")
        wait = instance_ping_cooldown_remaining(data, now)
        if wait > 0:
            record_ping_attempt(data, interaction, "rate_limited", None)
            save_json_log(INSTANCE_PING_LOG_PATH, "instance ping", data)
            return (
                f"Someone measured this {describe_wait(wait)} ago. One measurement "
                f"an hour, across the whole server - try again then.",
                None,
            )

        measured = await measure_instance_urls(plan_instance_pings(items))

        outcome = "measured" if measured else "nothing_to_measure"
        detail = None if measured else "no listed instance could be measured"
        record_ping_attempt(data, interaction, outcome, detail)
        # Recorded whether or not anything answered. The requests were made
        # either way, so charging for them is the honest accounting, and it also
        # stops a broken index from being retried in a loop by whoever is
        # curious enough to keep asking.
        data["lastCooldownAt"] = now.isoformat()
        save_json_log(INSTANCE_PING_LOG_PATH, "instance ping", data)

    unreachable = sum(1 for r in INSTANCE_PINGS.values() if r.get("error"))
    note = f"Measured {measured} instance{'s' if measured != 1 else ''} just now."
    if unreachable:
        note += f" {unreachable} did not answer."
    return None, note


def record_ping_attempt(data: dict, interaction, outcome, detail) -> None:
    """Append one forced measurement to the history."""
    data["entries"].append(
        {
            "at": datetime.now(timezone.utc).isoformat(),
            "discordId": str(interaction.user.id),
            "username": str(interaction.user),
            "guild": interaction.guild.name if interaction.guild else None,
            "outcome": outcome,
            "detail": detail,
        }
    )
    log.info("instance ping attempt: %s from %s", outcome, redact(str(interaction.user)))


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


def nsfw_param(channel):
    """The `isNsfw` filter value for a channel.

    "all" where Discord age-gates the channel, or where the admin listed it in
    NSFW_CHANNELS. "False" everywhere else.

    "all", not "True": being allowed to see NSFW is not a request for NSFW. An
    artist's catalogue is usually mixed, and "True" would return only the NSFW
    slice of it — so an artist with seven NSFW and a hundred SFW images would
    show seven, and the SFW work would become unreachable from an age-restricted
    channel. The channel says what the reader may see, so it asks for all of it.

    An explicit `!waifu nsfw` still means "True", because that is the user
    asking for NSFW specifically. Those are two different intents and they take
    two different values.
    """
    return "all" if nsfw_allowed(channel) else "False"


async def hidden_nsfw_total(path, params):
    """How many NSFW images a query matches, if any.

    Used only to decide whether to explain an empty result. Nothing from the
    response is rendered: an age-restricted image must never reach a channel
    Discord does not age-gate. A count is not content — the same number is
    already on the public endpoint for anyone who asks for it — so this tells
    a user *that* something is there without putting it on screen.

    isNsfw=True, not "all": "all" counts NSFW and SFW together, so a message
    built from it overstates what is being withheld.
    """
    status, body, _ = await api_request(
        "GET", path, base=API_BASE,
        params={**params, "isNsfw": "True", "pageSize": 1},
    )
    if status >= 400 or not isinstance(body, dict):
        return 0
    return body.get("totalCount") or 0


async def nothing_found(interaction, params, noun, name, plain):
    """Answer an empty result, saying why when the content is NSFW-gated."""
    hidden = await hidden_nsfw_total("images", params)
    if hidden and not nsfw_allowed(interaction.channel):
        await interaction.followup.send(
            f"{noun} `{name}` has {hidden} NSFW image(s) and nothing age-restricted "
            f"is shown here. Use a channel with *Age Restriction* enabled.",
            ephemeral=True,
        )
        return
    await interaction.followup.send(plain, ephemeral=True)


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
    "latency_at": None,      # monotonic, or None if nothing has answered yet
    "images": None,          # /stats/public totalImages
    "tags": None,            # /stats/public totalTags
    "artists": None,         # /stats/public totalArtists
    "served": 0,             # images this process has posted
    "last_id": None,         # ID of the most recent image posted
    "last_served_at": None,  # monotonic, or None if nothing has been served
    "errors": 0,             # failed API calls since start
    "last_error_at": None,   # monotonic, or None if nothing has failed yet
}

# How long a latency reading is still worth showing.
#
# There used to be a 60-second probe whose only real job was keeping the origin
# awake, and it refreshed this number as a side effect. That probe is gone, so
# latency now comes from whatever real traffic the bot happens to serve, and on
# a quiet day there may be none for hours. Without a window the status line would
# go on reporting "the API, 340ms away" from a reading taken this morning, which
# is the same confident-wrong-number problem the instance index refuses.
#
# Past this the slot returns None and the rotation falls back to a fixed line,
# which is the honest rendering of "I have no recent measurement".
LATENCY_FRESH_SECONDS = 15 * 60


def fresh_latency_ms():
    """A recent round trip in milliseconds, or None if there isn't one."""
    if STATS["latency_ms"] is None or STATS["latency_at"] is None:
        return None
    if time.monotonic() - STATS["latency_at"] > LATENCY_FRESH_SECONDS:
        return None
    return STATS["latency_ms"]


def note_ok(latency_ms):
    STATS["latency_ms"] = round(latency_ms)
    STATS["latency_at"] = time.monotonic()


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

    wanted_nsfw = "nsfw" in parts
    if wanted_nsfw and not nsfw_allowed(ctx.channel):
        await ctx.reply(f"This channel isn't an NSFW channel. {nsfw_hint(ctx.channel)}")
        return
    if wanted_nsfw:
        parts.remove("nsfw")

    params = {
        # An explicit `nsfw` is a request for NSFW; merely being in an
        # age-restricted channel is not. See nsfw_param for why those differ.
        "isNsfw": "True" if wanted_nsfw else nsfw_param(ctx.channel),
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
    params = {
        "isNsfw": nsfw_param(ctx.channel),
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
    latency = fresh_latency_ms()
    if latency is None:
        reading = (
            "no reading yet"
            if STATS["latency_at"] is None
            else f"last reading {human_duration(time.monotonic() - STATS['latency_at'])} ago"
        )
    else:
        reading = f"{latency}ms"
    embed.add_field(name="API latency", value=reading)
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

        # GET /images/{id} has no isNsfw filter — the endpoint serves whatever id
        # you name, filtered on review status only. So the bot is the only gate
        # here, and this check is load-bearing rather than defensive: without it
        # `/image <nsfw id>` posts age-restricted content into a channel Discord
        # does not age-gate, which is how a bot gets removed.
        if body.get("isNsfw") and not nsfw_allowed(interaction.channel):
            await interaction.followup.send(
                f"Image {image_id} is NSFW and this channel isn't age-restricted. "
                f"{nsfw_hint(interaction.channel)}",
                ephemeral=True,
            )
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
        params = {
            "includedTags": slug,
            "pageSize": 5,
            "isNsfw": nsfw_param(interaction.channel),
        }
        status, body, _ = await api_request("GET", "images", base=API_BASE, params=params)
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = (body.get("items") or [])[:5]
        if not items:
            await nothing_found(
                interaction, params, "Tag", slug, f"No images tagged `{slug}`."
            )
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
        params = {
            "includedArtists": name,
            "pageSize": 5,
            "isNsfw": nsfw_param(interaction.channel),
        }
        status, body, _ = await api_request("GET", "images", base=API_BASE, params=params)
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = (body.get("items") or [])[:5]
        if not items:
            await nothing_found(interaction, params, "Artist", name, f"No images by `{name}`.")
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


@bot.tree.command(
    name="instances", description="Other people's deployments, and whether they work."
)
@app_commands.describe(
    needed="Re-measure latency now. One per hour across the server.",
)
async def slash_instances(interaction: discord.Interaction, needed: bool = False):
    async def run():
        status, body, _ = await api_request("GET", "instances", base=ORIGIN_BASE)
        if status >= 400:
            await interaction.followup.send(f"The API answered HTTP {status}.", ephemeral=True)
            return
        items = body.get("instances") or []
        if not items:
            await interaction.followup.send("Nobody has registered an instance yet.")
            return

        note = None
        if needed:
            refusal, note = await force_instance_measurement(interaction, items)
            if refusal:
                await interaction.followup.send(refusal, ephemeral=True)
                return
        elif not INSTANCE_PINGS and INSTANCE_PING_SECONDS > 0:
            # Nothing measured yet: the bot has just started, or they are switched
            # off in the environment. Kick a sweep off in the background rather than
            # awaiting one here - the interaction is already deferred, but a reader
            # waiting on a cold Render instance is exactly the case where this
            # command is wanted to answer instantly. The lines below say what is
            # happening rather than showing a column of nothing.
            asyncio.create_task(refresh_instance_pings())

        now = time.monotonic()
        lines = []
        for entry in items[:20]:
            label = entry.get("name") or entry.get("url") or "?"
            url = entry.get("url") or ""
            # The signal is called "images" on GET /instances. Tri-state on
            # purpose: it stays null until the prober has run, and collapsing
            # that into a failure would claim we looked when we did not.
            images = entry.get("images")
            mark = {True: "serving", False: "image check failed"}.get(
                images, "not checked yet"
            )
            lines.append(f"[{label}]({url}) — {mark} · {describe_ping(url, now)}")
        body = "\n".join(lines)

        embed = discord.Embed(
            title=f"Instances ({len(items)})",
            # The note goes above the list rather than in the footer, which is
            # already carrying the provenance of the numbers.
            description=f"*{note}*\n\n{body}" if note else body,
            color=discord.Color.magenta(),
        )
        # Whose line these were measured on. The figure is about this bot's
        # connection to somebody else's server, which is neither the reader's
        # connection to it nor the index's, and an unlabelled millisecond number
        # invites exactly the wrong conclusion from the one person it is shown to.
        # text= is not optional decoration. Every setter on Embed is keyword-only,
        # so a positional string raises TypeError at send time rather than at
        # import, and a bot that has been running happily all day gives no other
        # warning that the command is broken.
        embed.set_footer(
            text=(
                f"Latency is this bot's round trip, measured every "
                f"{describe_age(INSTANCE_PING_SECONDS)}."
                if INSTANCE_PING_SECONDS > 0
                else "Latency measurement is switched off on this bot."
            )
        )
        await interaction.followup.send(embed=embed)

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
    bot.loop.create_task(refresh_stats())
    if INSTANCE_PING_SECONDS > 0:
        bot.loop.create_task(ping_instances())


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

# How often /stats/public is read for the catalogue lines in the status
# rotation. Half an hour: the totals move slowly, and reading them more often
# than that buys nothing.
#
# This replaced a 60-second interval, and the difference is the point rather than
# a regression. A 60s request to the origin was a keep-alive - it held a
# free-tier Render instance awake against its fifteen-minute spin-down, and cost
# a request a minute to do it. A separate service owns that now, so nothing here
# is trying to hold anything open.
STATS_REFRESH_SECONDS = 30 * 60

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
    ms = fresh_latency_ms()
    if ms is None:
        return None
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
        if fresh_latency_ms() is not None:
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


async def refresh_stats():
    """Keep the catalogue counters current without waiting for a command.

    This used to ping /ping every 60 seconds against the origin, and the ping's
    real job was keeping Render awake: a free-tier instance spins down after
    about fifteen idle minutes, and a 60-second request is what stopped it. A
    separate service does that job now, so the ping is gone.

    What is left is the reason to keep going at all - /stats/public feeds the
    catalogue lines in the status rotation, and those return None until
    something fills them in. Half an hour is far more often than the totals
    change, and far less often than a keep-alive.

    The origin is expected to be asleep when this runs, so the read carries the
    long cold-start timeout. Without it a spinning-down Render answers nothing
    inside the 30s default and a working API gets counted as an error.
    """
    await bot.wait_until_ready()
    while not bot.is_closed():
        try:
            counts = await api_get("stats/public", timeout=ORIGIN_TIMEOUT_SECONDS)
            STATS["images"] = counts.get("totalImages")
            STATS["tags"] = counts.get("totalTags")
            STATS["artists"] = counts.get("totalArtists")
        except Exception as e:
            # A failed refresh is already counted by api_get; the next round
            # retries, so there is nothing extra to record here.
            log.warning("stats refresh failed: %s", redact(str(e)))
        await asyncio.sleep(STATS_REFRESH_SECONDS)


async def ping_instances():
    """Keep the latency figures behind /instances current.

    A task rather than work done inside the command, because measuring means
    dialling a stranger's server and /instances is public: run inline, anybody in
    the server could choose when and how many outbound requests this bot makes,
    and a Render free-tier instance that had spun down would be paying a cold
    start on somebody else's behalf.

    The command reads the cache. The first sweep runs immediately rather than
    after the first interval, so a bot that has just started has something to
    show instead of reporting every entry as unmeasured for a quarter of an hour.
    """
    await bot.wait_until_ready()
    while not bot.is_closed():
        try:
            await refresh_instance_pings()
        except Exception as e:
            log.warning("instance sweep failed: %s", redact(str(e)))
        await asyncio.sleep(INSTANCE_PING_SECONDS)


bot.run(os.environ["DISCORD_TOKEN"], log_handler=None)
