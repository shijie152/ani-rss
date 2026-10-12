#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gremlins_version="v0.6.0"
# 每个变异体的超时是「本次基线耗时 × 系数」，默认系数在构建缓存热的时候会把
# 基线压到几十毫秒，于是大量变异体被误判成超时（实测同一份代码：默认系数
# 2 killed/70 timed out，系数 50 得到 57 killed/15 lived/0 timed out，与冷缓存
# 那轮完全一致）。固定系数让结果不再取决于缓存冷热。
timeout_coefficient=50

# 变异测试的成本 = 变异体数量 × 每个都要重新编译并跑该包测试（六包共约 1065
# 个变异体、8 分钟），所以只对「相对基线有改动」的包跑；基线取不到时回落成全量。
core_packages="rss torrent source metadata subscription outbound"
selected="$(bash "${repo_root}/scripts/changed-packages.sh" ${core_packages})"
if [ -z "$selected" ]; then
	printf 'Mutation testing skipped：相对基线没有改动到 %s\n' "${core_packages}"
	exit 0
fi

cd "${repo_root}/go-backend"
export GOWORK=off

run_go() {
	if command -v mise >/dev/null 2>&1; then
		mise exec -- go "$@"
	else
		go "$@"
	fi
}

for package in ${selected}; do
	printf 'Mutation testing internal/%s\n' "${package}"
	run_go run "github.com/go-gremlins/gremlins/cmd/gremlins@${gremlins_version}" unleash "./internal/${package}" --workers=2 --timeout-coefficient="${timeout_coefficient}"
done
