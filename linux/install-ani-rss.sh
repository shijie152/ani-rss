#!/usr/bin/env bash
set -euo pipefail
# ANI-RSS Go 一体化安装脚本 with Systemd 服务
# 适用系统: Ubuntu/Debian/CentOS/RHEL

# 定义颜色代码
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

# 定义常量
INSTALL_DIR="/opt/ani-rss"
SERVICE_USER="ani-rss"
SERVICE_NAME="ani-rss.service"
SERVER_PORT="7789"
RELEASE_BASE_URL="${RELEASE_BASE_URL:-https://github.com/wushuo894/ani-rss/releases}"
ANI_RSS_VERSION="${ANI_RSS_VERSION:-}"
ANI_RSS_VERSION="${ANI_RSS_VERSION#v}"

detect_asset() {
    case "$(uname -m)" in
        x86_64|amd64) echo "linux-amd64" ;;
        aarch64|arm64) echo "linux-arm64" ;;
        armv7l|armv7|armhf) echo "linux-armv7" ;;
        *)
            echo -e "${RED}不支持的 CPU 架构: $(uname -m)${NC}" >&2
            exit 1
            ;;
    esac
}

# 检查root权限
check_root() {
    [ "$EUID" -ne 0 ] && echo -e "${RED}错误：请使用sudo或以root运行${NC}" && exit 1
}

# 创建专用用户
create_user() {
    if id "$SERVICE_USER" &>/dev/null; then
        echo -e "${GREEN}用户 $SERVICE_USER 已存在${NC}"
    else
        useradd -r -s /bin/false "$SERVICE_USER" && \
        echo -e "${GREEN}已创建系统用户 $SERVICE_USER${NC}" || {
            echo -e "${RED}用户创建失败${NC}"
            exit 1
        }
    fi
}

# 部署应用文件
deploy_app() {
    echo -e "${YELLOW}正在部署应用程序...${NC}"
    mkdir -p "$INSTALL_DIR/config" "$INSTALL_DIR/ui"

    local asset="${ANI_RSS_ASSET:-$(detect_asset)}"
    local archive="ani-rss-${asset}.tar.gz"
    local url
    if [ -n "$ANI_RSS_VERSION" ]; then
        url="${RELEASE_BASE_URL}/download/v${ANI_RSS_VERSION}/${archive}"
    else
        url="${RELEASE_BASE_URL}/latest/download/${archive}"
    fi
    local temp bundle
    temp="$(mktemp -d)"
    echo "正在下载 ${archive}"
    if ! wget -q --show-progress "$url" -O "$temp/$archive"; then
        echo -e "${RED}下载 ${archive} 失败${NC}"
        exit 1
    fi
    tar -xzf "$temp/$archive" -C "$temp"
    bundle="$temp/ani-rss-${asset}"
    if [ ! -x "$bundle/ani-rss" ] || [ ! -f "$bundle/ui/index.html" ]; then
        echo -e "${RED}发布包内容不完整${NC}"
        exit 1
    fi
    install -m 0755 "$bundle/ani-rss" "$INSTALL_DIR/ani-rss"
    cp -a "$bundle/ui/." "$INSTALL_DIR/ui/"
    rm -rf "$temp"
    echo "Go 程序和 UI 部署完成"

    echo "正在下载 ani-rss.sh"
    # 下载管理脚本
    if ! wget -q https://github.com/wushuo894/ani-rss/raw/master/linux/ani-rss.sh -O "/usr/local/bin/ani-rss"; then
        echo -e "${RED}下载启动脚本失败${NC}"
        exit 1
    fi
    echo "下载完成 ani-rss.sh"

    chmod +x /usr/local/bin/ani-rss

    # 设置权限
    chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR"
    chmod 750 "$INSTALL_DIR"
    chmod 755 "$INSTALL_DIR/ani-rss"
    echo -e "${GREEN}程序部署完成${NC}"
}

configure_port() {
    echo -e "${YELLOW}正在配置端口...${NC}"
    echo -e "当前默认端口: $SERVER_PORT"
    read -p "是否使用默认端口 $SERVER_PORT? [Y/n]: " choice
    case "$choice" in
        [Nn]*)
            while true; do
                read -p "请输入端口号(1-65535): " input_port
                if [[ "$input_port" =~ ^[0-9]+$ ]] && [ "$input_port" -ge 1 ] && [ "$input_port" -le 65535 ]; then
                    SERVER_PORT="$input_port"
                    break
                else
                    echo -e "${RED}端口无效${NC}"
                fi
            done
        ;;
    esac
    echo -e "${GREEN}已选择端口: $SERVER_PORT${NC}"
}

# 配置系统服务
setup_service() {
    echo -e "${YELLOW}正在配置系统服务...${NC}"
    tee /etc/systemd/system/"$SERVICE_NAME" > /dev/null <<EOF
[Unit]
Description=ANI-RSS Service
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/ani-rss --listen 0.0.0.0:$SERVER_PORT --ui-dir $INSTALL_DIR/ui --config-dir $INSTALL_DIR/config
Restart=on-failure
RestartSec=30
LimitNOFILE=65535
Environment="TZ=Asia/Shanghai"
Environment="CONFIG=$INSTALL_DIR/config"
Environment="LISTEN_ADDR=0.0.0.0:$SERVER_PORT"
Environment="UI_DIR=$INSTALL_DIR/ui"
Environment="GO_DOMAINS=state,runtime,subscriptions,sources,rss,media,rename,maintenance"
Environment="SWAGGER_ENABLED=false"
Environment="MCP_ENABLED=false"

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME" > /dev/null 2>&1

    if ! systemctl start "$SERVICE_NAME"; then
        echo -e "${RED}服务启动失败，请检查日志：journalctl -u $SERVICE_NAME${NC}"
        exit 1
    fi
    echo -e "${GREEN}系统服务配置完成${NC}"
}

# 验证安装
verify_install() {
    echo -e "\n${YELLOW}验证安装...${NC}"
    if ! systemctl is-active "$SERVICE_NAME" | grep -q "active"; then
        echo -e "${RED}服务未正常运行${NC}"
        exit 1
    fi

    echo -e "${GREEN}验证通过，服务运行正常${NC}"
}

# 显示访问信息
show_info() {
    IP=$(hostname -I | awk '{print $1}')
    echo -e "\n${GREEN}安装完成！访问信息："
    echo -e "URL: http://$IP:$SERVER_PORT"
    echo -e "用户名: admin"
    echo -e "初始密码: admin${NC}"
    echo -e "${RED}请务必及时修改默认用户名与密码${NC}"
    ani-rss help
}

# 主流程
main() {
    check_root
    create_user
    deploy_app
    configure_port
    setup_service
    verify_install
    show_info
}

main
