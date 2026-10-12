#!/usr/bin/env bash
# 按改动挑选需要跑核心测试（fuzz / 变异）的包。
#
# 输出：空格分隔的包名，无相关改动时输出为空。基线取不到、无法比对，或改动
# 落在被这些包共享的代码（go-backend 下其他目录、go.mod、核心测试脚本）上时，
# 输出全部包——宁可多测，不可漏测。
#
# 基线优先级：$CORE_TEST_BASE → origin/main → HEAD~1
# 强制全量：CORE_TEST_ALL=1
# 直接给出改动文件（跳过 git 比对，供测试与排查用）：CORE_TEST_FILES
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
packages=("$@")

if [ "${#packages[@]}" -eq 0 ]; then
	printf '用法: %s <包名...>\n' "$(basename "$0")" >&2
	exit 2
fi

all() { printf '%s\n' "${packages[*]}"; }

if [ "${CORE_TEST_ALL:-0}" != "0" ]; then
	echo "CORE_TEST_ALL 已设置，跑全部包" >&2
	all
	exit 0
fi

base="${CORE_TEST_BASE:-}"
if [ -z "$base" ]; then
	for candidate in origin/main HEAD~1; do
		if git -C "$repo_root" rev-parse --verify --quiet "${candidate}^{commit}" >/dev/null; then
			base="$candidate"
			break
		fi
	done
fi

if [ -z "$base" ]; then
	echo "找不到可比对的基线，跑全部包" >&2
	all
	exit 0
fi

if [ -n "${CORE_TEST_FILES:-}" ]; then
	changed="$CORE_TEST_FILES"
else
	if ! changed="$(git -C "$repo_root" diff --name-only "${base}...HEAD" 2>/dev/null)"; then
		echo "与基线 ${base} 无法比对，跑全部包" >&2
		all
		exit 0
	fi
fi

if [ -z "$changed" ]; then
	echo "相对 ${base} 无改动，跳过" >&2
	exit 0
fi

selected=()
shared=""
is_tracked() {
	local candidate
	for candidate in "${packages[@]}"; do
		[ "$candidate" = "$1" ] && return 0
	done
	return 1
}

while IFS= read -r file; do
	[ -z "$file" ] && continue
	case "$file" in
	go-backend/internal/*/*)
		pkg="${file#go-backend/internal/}"
		pkg="${pkg%%/*}"
		if is_tracked "$pkg"; then
			case " ${selected[*]:-} " in
			*" $pkg "*) ;;
			*) selected+=("$pkg") ;;
			esac
		else
			shared="$file"
		fi
		;;
	go-backend/*) shared="$file" ;;
	scripts/changed-packages.sh | scripts/test-core-*.sh) shared="$file" ;;
	esac
done <<<"$changed"

if [ -n "$shared" ]; then
	echo "改动涉及共享代码（${shared}），跑全部包" >&2
	all
	exit 0
fi

if [ "${#selected[@]}" -eq 0 ]; then
	echo "改动不涉及核心包，跳过" >&2
	exit 0
fi

echo "按改动选中：${selected[*]}" >&2
printf '%s\n' "${selected[*]}"
