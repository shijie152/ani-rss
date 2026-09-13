#!/usr/bin/env bash

# Go release version helper
# 用法: ./new_version.sh <new_version>

set -e  # 遇到错误立即退出

# 检查是否传入版本号参数
if [ $# -ne 1 ]; then
    echo "错误: 请指定版本号参数"
    echo "用法: $0 <new_version>"
    echo "示例: $0 2.4.9"
    exit 1
fi

VERSION="$1"

# 校验版本号格式
if ! [[ $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9_-]+)?$ ]]; then
    echo "错误: 版本号格式无效 '$VERSION'"
    echo "期望格式: 主版本号.次版本号.修订号[-限定符]"
    echo "有效示例: 2.4.9, 1.0.0-SNAPSHOT, 3.5.2-RELEASE"
    exit 1
fi

echo "正在将项目版本设置为: $VERSION"
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
printf '%s\n' "$VERSION" > "$SCRIPT_DIR/VERSION"
echo "✅ Go 版本已更新为: $VERSION"
