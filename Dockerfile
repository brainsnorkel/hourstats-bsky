FROM golang:1.27-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /hourstats ./cmd/hourstats
# One-off admin tool for the sentiment realignment; see docs/SENTIMENT_REALIGNMENT_PLAN.md
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /realign ./cmd/realign

FROM alpine:3.21

# su-exec drops privileges from the entrypoint; see the ENTRYPOINT note below.
RUN apk add --no-cache ca-certificates tzdata sqlite su-exec \
    && addgroup -g 1000 hourstats \
    && adduser -D -u 1000 -G hourstats hourstats

COPY --from=builder /hourstats /usr/local/bin/hourstats
COPY --from=builder /realign /usr/local/bin/realign

# The entrypoint is written here rather than COPYed because .dockerignore is an
# allowlist (only go.mod, go.sum, cmd, internal and this file reach the remote
# builder).
#
# The bot itself must not run as root: it parses untrusted firehose JSON and
# writes heap profiles next to the database. But Fly mounts the volume at /data
# owned by root:root, and a process running as uid 1000 cannot create the
# database there. So the container starts as root, fixes /data ownership if it
# has not been fixed already, and execs the real binary through su-exec as
# hourstats. Every subsequent boot only stats the directory.
#
# There is deliberately no `USER hourstats` directive: it would run the
# entrypoint as uid 1000, which can neither chown the root-owned volume nor
# drop privileges. The guarantee is enforced at exec time instead — `docker run
# <image> id` prints uid=1000(hourstats). The entrypoint also copes with being
# started as non-root (`docker run --user`), in which case it skips the chown
# and the su-exec and execs directly.
#
# `fly ssh console -C "realign -dry-run"` is unaffected: Fly's ssh sessions are
# spawned by the init as root and never go through the entrypoint, which is what
# realign needs — it writes a local backup and rewrites history in place.
RUN printf '%s\n' \
    '#!/bin/sh' \
    'set -e' \
    '' \
    'if [ "$(id -u)" = 0 ] && [ -d /data ]; then' \
    '    if [ "$(stat -c %u /data)" != 1000 ]; then' \
    '        echo "entrypoint: /data is not owned by hourstats, chowning" >&2' \
    '        chown -R hourstats:hourstats /data' \
    '    fi' \
    'fi' \
    '' \
    'if [ "$#" -eq 0 ]; then' \
    '    set -- /usr/local/bin/hourstats' \
    'fi' \
    '' \
    'if [ "$(id -u)" = 0 ]; then' \
    '    exec su-exec hourstats:hourstats "$@"' \
    'fi' \
    'exec "$@"' \
    > /usr/local/bin/docker-entrypoint.sh \
    && chmod 0755 /usr/local/bin/docker-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
