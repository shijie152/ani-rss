#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UI_DIR="${ROOT_DIR}/ani-rss-ui"
OUT_DIR="${ROOT_DIR}/dist/go-release"
VERSION="${ANI_RSS_VERSION:-$(sed -n '1p' "${ROOT_DIR}/VERSION" 2>/dev/null || true)}"
VERSION="${VERSION:-dev}"

if ! command -v tar >/dev/null 2>&1 || ! command -v zip >/dev/null 2>&1; then
  echo "tar and zip are required for release packaging" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required" >&2
  exit 1
fi

if [ -f "${UI_DIR}/dist/index.html" ] && [ "${FORCE_UI_BUILD:-0}" != "1" ]; then
  echo "Using existing UI build at ${UI_DIR}/dist"
elif command -v pnpm >/dev/null 2>&1; then
  pnpm --dir "${UI_DIR}" install --frozen-lockfile
  pnpm --dir "${UI_DIR}" build
else
  echo "pnpm is required when ani-rss-ui/dist is absent" >&2
  exit 1
fi

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

build_one() {
  local name="$1" os="$2" arch="$3" arm="${4:-}"
  local staging="${OUT_DIR}/${name}"
  mkdir -p "${staging}/ui"
  if [ -n "${arm}" ]; then
    GOOS="${os}" GOARCH="${arch}" GOARM="${arm}" CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "${staging}/ani-rss" "${ROOT_DIR}/go-backend/cmd/ani-rss"
  else
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "${staging}/ani-rss" "${ROOT_DIR}/go-backend/cmd/ani-rss"
  fi
  cp -R "${UI_DIR}/dist/." "${staging}/ui/"
  cat > "${staging}/README.txt" <<EOF
ANI-RSS ${VERSION}

Start: ./ani-rss --ui-dir ./ui --config-dir ./config
Data:  ./config/ani-rss.sqlite
The existing Vue UI is bundled unchanged under ./ui.
EOF
  case "${os}" in
    windows) mv "${staging}/ani-rss" "${staging}/ani-rss.exe"; (cd "${OUT_DIR}" && zip -q -r "${name}.zip" "${name}") ;;
    darwin) (cd "${OUT_DIR}" && tar -czf "${name}.tar.gz" "${name}") ;;
    linux) (cd "${OUT_DIR}" && tar -czf "${name}.tar.gz" "${name}") ;;
  esac
}

build_one "ani-rss-linux-amd64" linux amd64
build_one "ani-rss-linux-arm64" linux arm64
build_one "ani-rss-linux-armv7" linux arm 7
build_one "ani-rss-windows-amd64" windows amd64
build_one "ani-rss-macos-amd64" darwin amd64
build_one "ani-rss-macos-arm64" darwin arm64

echo "Go release bundles written to ${OUT_DIR}"
