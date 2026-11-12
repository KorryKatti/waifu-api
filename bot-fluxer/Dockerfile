# syntax=docker/dockerfile:1

# Multi-stage build producing a small runtime image.
#
# The bot is a single static Go binary: it makes outbound HTTPS/WebSocket
# calls to Fluxer and the waifu.im API and listens on no port. CGO is
# disabled so the binary has no libc dependency.

# ---- Build ----------------------------------------------------------------
FROM golang:1.23-alpine AS build

WORKDIR /src

# Cache dependency download separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/bot-fluxer .

# ---- Runtime --------------------------------------------------------------
# Alpine (rather than distroless) so the named volume at /data is writable by
# the non-root user without the host having to pre-create it root-owned.
# ca-certificates is required for outbound TLS to Fluxer and the waifu.im API.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 bot \
 && mkdir -p /data \
 && chown bot:bot /data

COPY --from=build /out/bot-fluxer /bot-fluxer

USER bot
WORKDIR /data

# State files (register_log.json, instance_ping_log.json) live on the volume
# mounted at /data by docker-compose.yml, so cooldowns survive restarts.
ENV BOT_STATE_DIR=/data

# No EXPOSE: the bot listens on nothing. It connects outbound to the Fluxer
# gateway (WebSocket) and the waifu.im / Fluxer APIs (HTTPS).
ENTRYPOINT ["/bot-fluxer"]
