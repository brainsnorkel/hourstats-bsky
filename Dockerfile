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
# database there. So the container starts as root, fixes the ownership of
# anything under /data that is not the app user's, and execs the real binary
# through su-exec as hourstats.
#
# The check is on the files, not on /data itself: root-run maintenance inside
# the volume — a `fly ssh console -C realign`, a backup written by hand — leaves
# root-owned files in a directory that is already owned by hourstats, and the
# uid-1000 process then cannot write them. `find ! -user hourstats` is a no-op
# when the volume is clean, and -maxdepth 2 keeps it to the files that matter
# (the db and its -wal/-shm, backups/, the memguard dumps) rather than walking a
# large backups tree on every boot.
#
# There is deliberately no `USER hourstats` directive: it would run the
# entrypoint as uid 1000, which can neither chown the root-owned volume nor
# drop privileges. The guarantee is enforced at exec time instead — `docker run
# <image> id` prints uid=1000(hourstats). The entrypoint also copes with being
# started as non-root (`docker run --user`), in which case it skips the chown
# and the su-exec and execs directly.
#
# `fly ssh console -C "realign -dry-run"` is unaffected: Fly's ssh sessions are
# spawned by the init as root and never go through the entrypoint. Prefer
# `fly ssh console -C "su-exec hourstats realign -dry-run"` all the same, so the
# backup and the rewrite land as the app user and the next boot has nothing to
# chown.
RUN printf '%s\n' \
    '#!/bin/sh' \
    'set -e' \
    '' \
    'if [ "$(id -u)" = 0 ] && [ -d /data ]; then' \
    '    find /data -maxdepth 2 ! -user hourstats -exec chown hourstats:hourstats {} +' \
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
