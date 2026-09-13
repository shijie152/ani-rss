#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "${BIN_DIR}"' EXIT

for target in \
  "linux/amd64/" "linux/arm64/" "linux/arm/7" \
  "windows/amd64/" "darwin/amd64/" "darwin/arm64/"; do
  IFS=/ read -r os arch arm <<<"${target}"
  output="${BIN_DIR}/ani-rss-${os}-${arch}${arm:+-${arm}}"
  if [ "${os}" = windows ]; then output="${output}.exe"; fi
  if [ -n "${arm:-}" ]; then
    GOOS="${os}" GOARCH="${arch}" GOARM="${arm}" CGO_ENABLED=0 go build -o "${output}" "${ROOT_DIR}/go-backend/cmd/ani-rss"
  else
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go build -o "${output}" "${ROOT_DIR}/go-backend/cmd/ani-rss"
  fi
  test -s "${output}"
done

go test ./go-backend/...
go test -race ./go-backend/...
go vet ./go-backend/...

runtime_dir="$(mktemp -d)"
runtime_binary="${runtime_dir}/ani-rss"
runtime_config="${runtime_dir}/config"
runtime_log="${runtime_dir}/ani-rss.log"
trap 'kill "${runtime_pid:-}" 2>/dev/null || true; rm -rf "${runtime_dir}"' EXIT
mkdir -p "${runtime_config}"
CGO_ENABLED=0 go build -trimpath -o "${runtime_binary}" "${ROOT_DIR}/go-backend/cmd/ani-rss"
"${runtime_binary}" --listen 127.0.0.1:17789 --ui-dir "${ROOT_DIR}/ani-rss-ui/dist" --config-dir "${runtime_config}" --go-domains runtime >"${runtime_log}" 2>&1 &
runtime_pid=$!
ready=0
for _ in $(seq 1 20); do
  if curl --fail --silent http://127.0.0.1:17789/api/ping >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
if [ "${ready}" -ne 1 ]; then
  cat "${runtime_log}" >&2
  exit 1
fi
curl --fail --silent http://127.0.0.1:17789/ | grep -q '<!doctype html'
echo "Go cross-build and test smoke passed"
