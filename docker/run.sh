#!/bin/sh
set -eu

export LANG="${LANG:-C.UTF-8}"
export LC_ALL="${LC_ALL:-C.UTF-8}"
export CONFIG="${CONFIG:-/config}"
export SERVER_PORT="${SERVER_PORT:-7789}"
export LISTEN_ADDR="${LISTEN_ADDR:-${SERVER_ADDRESS:-0.0.0.0}:${SERVER_PORT}}"

mkdir -p "${CONFIG}"
exec /usr/app/ani-rss \
  --listen "${LISTEN_ADDR}" \
  --ui-dir /usr/app/ui \
  --config-dir "${CONFIG}"
