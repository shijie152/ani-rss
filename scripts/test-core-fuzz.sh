#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fuzz_time="${FUZZ_TIME:-5s}"
cd "${repo_root}/go-backend"
export GOWORK=off

run_go() {
	if command -v mise >/dev/null 2>&1; then
		mise exec -- go "$@"
	else
		go "$@"
	fi
}

run_go test ./internal/rss -run='^$' -fuzz='^FuzzParseRSS$' -fuzztime="${fuzz_time}"
run_go test ./internal/rss -run='^$' -fuzz='^FuzzMatchMaintainsResourceInvariants$' -fuzztime="${fuzz_time}"
run_go test ./internal/rss -run='^$' -fuzz='^FuzzPlanSubmissionIsIdempotent$' -fuzztime="${fuzz_time}"
run_go test ./internal/torrent -run='^$' -fuzz='^FuzzParseMetainfoNeverReturnsUnsafeSuccess$' -fuzztime="${fuzz_time}"
run_go test ./internal/source -run='^$' -fuzz='^FuzzParseMikanHTML$' -fuzztime="${fuzz_time}"
run_go test ./internal/subscription -run='^$' -fuzz='^FuzzValidateItemsMatchesBoundaryRules$' -fuzztime="${fuzz_time}"
