#!/usr/bin/env bash
set -euo pipefail

# Build the UI unchanged and produce Go-only release bundles. The script is
# intentionally usable on Linux/macOS CI runners; cross compilation happens
# in Go and packaging that needs native platform tools is left to that runner.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${ROOT_DIR}/scripts/build-go-release.sh" "$@"
