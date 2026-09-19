#!/usr/bin/env bash
# Reproduce the CI quality gate locally in one command.
# Mirrors .github/workflows/build-test.yml plus gofmt and whitespace checks.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "==> go test ./go-backend/..."
go test ./go-backend/...

echo "==> go test -race ./go-backend/..."
go test -race ./go-backend/...

echo "==> go vet ./go-backend/..."
go vet ./go-backend/...

echo "==> gofmt -l go-backend"
unformatted="$(gofmt -l go-backend)"
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> git diff --check"
git diff --check

echo "All checks passed."
