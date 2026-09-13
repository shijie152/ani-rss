#!/bin/sh
set -eu

umask "${UMASK:-022}"
config="${CONFIG:-/config}"
mkdir -p "$config"

if [ "${PUID:-0}" != "0" ] || [ "${PGID:-0}" != "0" ]; then
  chown -R "${PUID:-0}:${PGID:-0}" "$config"
  exec su-exec "${PUID:-0}:${PGID:-0}" /run.sh
fi

exec /run.sh
