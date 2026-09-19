#!/usr/bin/env bash
# End-to-end lifecycle soak: RSS submission -> downloader completion -> media
# organize -> notification -> subscription finish/auto-disable.
#
# Runs go-backend/internal/soak, which drives the real Submitter and the real
# completion.Coordinator through a single in-memory downloader. This is the
# automated version of the manual qBittorrent soak — it exists so the
# "does Go really replace Java" question is a one-command check, and so a
# downloader-contract regression (like the HTTP-202 add semantics, eb0f1390)
# is caught before it reaches a live soak.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"
go test -v ./go-backend/internal/soak/...
