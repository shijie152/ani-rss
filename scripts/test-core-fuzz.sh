#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fuzz_time="${FUZZ_TIME:-5s}"

# 与变异测试同一套选包规则：只 fuzz 相对基线有改动的包。
core_packages="rss torrent source subscription"
selected="$(bash "${repo_root}/scripts/changed-packages.sh" ${core_packages})"
if [ -z "$selected" ]; then
	printf 'Fuzz skipped：相对基线没有改动到 %s\n' "${core_packages}"
	exit 0
fi

has() { case " ${selected} " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

cd "${repo_root}/go-backend"
export GOWORK=off

run_go() {
	if command -v mise >/dev/null 2>&1; then
		mise exec -- go "$@"
	else
		go "$@"
	fi
}

if has rss; then
	run_go test ./internal/rss -run='^$' -fuzz='^FuzzParseRSS$' -fuzztime="${fuzz_time}"
	run_go test ./internal/rss -run='^$' -fuzz='^FuzzMatchMaintainsResourceInvariants$' -fuzztime="${fuzz_time}"
	run_go test ./internal/rss -run='^$' -fuzz='^FuzzPlanSubmissionIsIdempotent$' -fuzztime="${fuzz_time}"
fi
if has torrent; then
	run_go test ./internal/torrent -run='^$' -fuzz='^FuzzParseMetainfoNeverReturnsUnsafeSuccess$' -fuzztime="${fuzz_time}"
fi
if has source; then
	run_go test ./internal/source -run='^$' -fuzz='^FuzzParseMikanHTML$' -fuzztime="${fuzz_time}"
fi
if has subscription; then
	run_go test ./internal/subscription -run='^$' -fuzz='^FuzzValidateItemsMatchesBoundaryRules$' -fuzztime="${fuzz_time}"
fi
