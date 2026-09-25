#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gremlins_version="v0.6.0"
cd "${repo_root}/go-backend"
export GOWORK=off

run_go() {
	if command -v mise >/dev/null 2>&1; then
		mise exec -- go "$@"
	else
		go "$@"
	fi
}

for package in rss torrent source metadata subscription; do
	printf 'Mutation testing internal/%s\n' "${package}"
	run_go run "github.com/go-gremlins/gremlins/cmd/gremlins@${gremlins_version}" unleash "./internal/${package}" --workers=2
done
