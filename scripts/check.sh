#!/usr/bin/env bash
# Reproduce the CI quality gate locally in one command.
# Mirrors .github/workflows/build-test.yml: Go checks (plus gofmt and whitespace
# checks) and, when the UI dependencies are installed, the frontend gates.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "==> go test ./go-backend/..."
go test ./go-backend/...

echo "==> go test -race ./go-backend/..."
go test -race ./go-backend/...

echo "==> go vet ./go-backend/..."
go vet ./go-backend/...

# gui 构建标签只在发布 workflow 里编译，本地门也要覆盖：托盘代码平时跑不到。
echo "==> go test -tags gui ./go-backend/internal/desktop/..."
go test -tags gui ./go-backend/internal/desktop/...

echo "==> go build -tags gui ./go-backend/cmd/ani-rss"
go build -tags gui -o "$(mktemp -d)/ani-rss-gui" ./go-backend/cmd/ani-rss

echo "==> gofmt -l go-backend"
unformatted="$(gofmt -l go-backend)"
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> git diff --check"
git diff --check

# CI 里前端是独立步骤；本地只在依赖已安装时跑，避免把 install 塞进质量门。
if [ -d ani-rss-ui/node_modules ]; then
  echo "==> pnpm --dir ani-rss-ui test"
  pnpm --dir ani-rss-ui test

  echo "==> pnpm --dir ani-rss-ui test:mutation"
  pnpm --dir ani-rss-ui test:mutation

  echo "==> pnpm --dir ani-rss-ui build"
  pnpm --dir ani-rss-ui build

  # 浏览器门需要 chromium；没装就跳过并说明，避免本地静默漏测。
  # 注意：playwright install --dry-run 无论是否已安装都返回 0，不能用它探测。
  if ls "${HOME}"/Library/Caches/ms-playwright/chromium-* >/dev/null 2>&1 \
      || ls "${HOME}"/.cache/ms-playwright/chromium-* >/dev/null 2>&1; then
    echo "==> pnpm --dir ani-rss-ui test:browser"
    pnpm --dir ani-rss-ui test:browser
  else
    echo "==> skip browser gate (chromium missing; run pnpm --dir ani-rss-ui exec playwright install chromium)"
  fi
else
  echo "==> skip frontend gates (ani-rss-ui/node_modules missing; run pnpm --dir ani-rss-ui install)"
fi

echo "All checks passed."
