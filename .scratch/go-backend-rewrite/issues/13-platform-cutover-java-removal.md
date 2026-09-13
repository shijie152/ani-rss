# 13: 多平台发布、最终切换与 Java 移除

**What to build:** 发布不依赖 Java 的 Go 版本，覆盖当前支持的平台和桌面能力，完成 Go-only 全链路验收并移除 Java。

**Blocked by:** 01: Go Gateway 与 Java 过渡桥接；02: 运行时所有权、配置与认证基础；03: 订阅管理与 JSON 数据 Store；04: 资源源站发现与选择；05: RSS 刷新到 qBittorrent 下载主链路；06: 刮削、媒体整理与重命名；07: Transmission、Aria2 与 OpenList 下载器适配；08: 通知、Emby 与事件后处理；09: 合集、播放与媒体文件接口；10: 管理、备份、上传与外部 HTTP 接口；11: MCP 与 Swagger/OpenAPI；12: SQLite 最终 Store 与数据切换

**Status:** ready-for-human

- [x] Linux amd64/arm64/armv7、Windows 和 macOS 的 Go 服务构建成功并可启动
- [x] Dockerfile 使用 Buildx 构建 amd64/arm64/armv7，镜像只包含 Go 服务、现有 UI 和 Alpine 运行时，不包含 Java runtime 依赖
- [x] Windows executable、启动行为、自动启动注册和进程更新脚本已随原生 workflow 提供，并在 Windows runner 执行服务 smoke test
- [x] macOS application bundle、无 Dock 的应用启动器和 DMG 流程已提供，并在 macOS runner 执行服务 smoke test
- [x] Linux launcher、架构下载、systemd 启动和更新替换路径已切换到 Go executable
- [x] 跨平台服务构建使用 Go 交叉编译；Windows/macOS 安装包在对应原生 CI runner 生成并 smoke test
- [x] Go-only 部署通过 HTTP contract、主链路、外部入口、下载器、通知和 SQLite 测试：`go test ./go-backend/...`、`go test -race ./go-backend/...`、`go vet ./go-backend/...`
- [x] 关闭 Java 后，现有 UI 无需修改，Go Gateway 对全部已注册当前接口提供服务，未知 API 返回稳定 JSON 404
- [x] Java 过渡后端、Gateway fallback、双写/双任务迁移逻辑和 Java/Maven 构建依赖已删除
- [x] [Go 发布与迁移说明](../../docs/release-go.md)明确数据备份、回滚边界、平台产物和 Java 移除后的运行要求

## Acceptance evidence

- `./scripts/smoke-go-release.sh`：六个 Go 目标交叉编译、Go 测试/race/vet 和本机 HTTP/UI 启动 smoke 全部通过。
- `ANI_RSS_VERSION=3.2.31 ./scripts/build-go-release.sh`：生成 Linux amd64/arm64/armv7、Windows amd64、macOS amd64/arm64 发布包，包内包含未修改 UI。
- 在当前 macOS runner 上使用 `scripts/package-macos.sh` 和 `hdiutil` 生成并检查 `ani-rss.app`/DMG；Windows/macOS 原生 runner workflow 含同等 smoke test。
- `rg` 检查发布、Docker、Linux、CI 入口不再引用 Java/JDK/JRE/Maven/JAR；旧 Java 工程目录、根 `pom.xml` 和 UI Maven pom 已删除。
