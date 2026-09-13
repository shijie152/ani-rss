# Go 版本发布与迁移说明

ANI-RSS 的运行时是单一 Go 服务，现有 Vue UI 保持不变。服务默认监听
`7789`，通过 `--ui-dir` 和 `--config-dir` 指定 UI 与数据目录；数据目录中的
`ani-rss.sqlite` 是唯一运行时业务数据源。

## 发布产物

`./package.sh` 构建并打包以下服务端产物：

- Linux amd64、arm64、armv7：`.tar.gz`
- Windows amd64：`.zip`，包含 `ani-rss.exe`
- macOS amd64、arm64：`.tar.gz`

macOS 原生 runner 使用 `scripts/package-macos.sh` 生成 `.app` 和 DMG；Windows
原生 runner 使用 `scripts/platform/windows` 中的启动、自动启动和更新脚本生成
Windows 包。对应 workflow 会执行原生启动 smoke test。

## Docker

`docker/Dockerfile` 使用 Buildx 生成 `linux/amd64`、`linux/arm64` 和
`linux/arm/v7` 镜像。镜像中只包含 Go 可执行文件、Vue 静态资源和 Alpine
运行时，不需要 JDK、JRE 或 Java archive。`/config` 仍是持久化数据卷。

## Linux 安装与更新

`linux/install-ani-rss.sh` 根据 `uname -m` 下载对应 Go 压缩包，systemd 直接
启动 `/opt/ani-rss/ani-rss`。升级时先备份 `/opt/ani-rss/config/ani-rss.sqlite`
及必要的媒体目录，再替换二进制和 UI；若新版本无法启动，停止服务并恢复原
二进制即可回滚，SQLite 数据不要在未备份时覆盖。

## 数据与回滚边界

旧 JSON 文件只在首次初始化 SQLite 时导入，之后不再作为运行时写入源。升级前
建议使用 UI 的配置备份或 `/api/exportConfig` 保存 ZIP。Go-only 版本不再启动或
转发到 Java；回滚只能回到已经支持 SQLite 的 Go 版本，不能把同一份正在使用的
SQLite 状态交给旧 Java 版本继续写入。

## 本地验收

```sh
go test ./go-backend/...
go test -race ./go-backend/...
go vet ./go-backend/...
./scripts/smoke-go-release.sh
```
