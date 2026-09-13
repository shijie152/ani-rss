#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <server-binary> <ui-directory> <output-directory>" >&2
  exit 2
fi

SERVER="$1"
UI="$2"
OUTPUT="$3"
APP="$OUTPUT/ani-rss.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
install -m 0755 "$SERVER" "$APP/Contents/MacOS/ani-rss-server"
install -m 0755 "$(dirname "$0")/platform/macos/start-ani-rss" "$APP/Contents/MacOS/ani-rss"
cp -R "$UI/." "$APP/Contents/Resources/ui/"
cp "$(dirname "$0")/platform/macos/Info.plist" "$APP/Contents/Info.plist"
if [ "$(uname -s)" = "Darwin" ]; then
  chmod -R 0755 "$APP"
  xattr -cr "$APP" 2>/dev/null || true
fi
echo "$APP"
