#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gremlins_version="v0.6.0"
# 每个变异体的超时是「本次基线耗时 × 系数」，默认系数在构建缓存热的时候会把
# 基线压到几十毫秒，于是大量变异体被误判成超时（实测同一份代码：默认系数
# 2 killed/70 timed out，系数 50 得到 57 killed/15 lived/0 timed out，与冷缓存
# 那轮完全一致）。固定系数让结果不再取决于缓存冷热。
timeout_coefficient=50
# 有效性下限：变异测试现在按天定时跑，日志没人看就等于没跑，所以给一条保守的
# 门槛让它能红。实测各包 63%–84%，55 是「测试被改弱了」的信号线，不是目标值。
# 超时不计入有效性（机器慢会让有效性偏高），所以这条门槛不会因 CI 抖动误报。
# 不用 gremlins 自带的 --threshold-efficacy：实测该标志绑到 viper 后不生效
# （传 99 仍然 exit 0），所以在脚本里自己比对输出，可验证、不依赖上游行为。
efficacy_threshold="${EFFICACY_THRESHOLD:-55}"

# 变异测试的成本 = 变异体数量 × 每个都要重新编译并跑该包测试（六包共约 1065
# 个变异体、8 分钟），所以只对「相对基线有改动」的包跑；基线取不到时回落成全量。
core_packages="rss torrent source metadata subscription outbound"
if [ "$#" -gt 0 ]; then
	# 显式给出包名（CI 矩阵逐个包调用）：跑指定的包，不再做改动比对。
	selected="$*"
else
	selected="$(bash "${repo_root}/scripts/changed-packages.sh" ${core_packages})"
fi
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
	if ! output="$(run_go run "github.com/go-gremlins/gremlins/cmd/gremlins@${gremlins_version}" unleash "./internal/${package}" \
		--workers=2 --timeout-coefficient="${timeout_coefficient}" 2>&1)"; then
		printf '%s\n' "${output}"
		exit 1
	fi
	printf '%s\n' "${output}"
	efficacy="$(printf '%s\n' "${output}" | sed -n 's/^Test efficacy: \([0-9.]*\)%.*/\1/p' | tail -1)"
	if [ -n "${efficacy}" ] && awk "BEGIN{exit !(${efficacy} < ${efficacy_threshold})}"; then
		printf 'FAIL: internal/%s 的有效性 %s%% 低于门槛 %s%%\n' "${package}" "${efficacy}" "${efficacy_threshold}" >&2
		exit 1
	fi
done
