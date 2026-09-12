# 13: 多平台发布、最终切换与 Java 移除

**What to build:** 发布不依赖 Java 的 Go 版本，覆盖当前支持的平台和桌面能力，完成 Go-only 全链路验收并移除 Java。

**Blocked by:** 01: Go Gateway 与 Java 过渡桥接；02: 运行时所有权、配置与认证基础；03: 订阅管理与 JSON 数据 Store；04: 资源源站发现与选择；05: RSS 刷新到 qBittorrent 下载主链路；06: 刮削、媒体整理与重命名；07: Transmission、Aria2 与 OpenList 下载器适配；08: 通知、Emby 与事件后处理；09: 合集、播放与媒体文件接口；10: 管理、备份、上传与外部 HTTP 接口；11: MCP 与 Swagger/OpenAPI；12: SQLite 最终 Store 与数据切换

**Status:** ready-for-agent

- [ ] Linux amd64/arm64/armv7、Windows 和 macOS 的 Go 服务构建成功并可启动
- [ ] Docker amd64/arm64/armv7 镜像可以运行现有 UI 和完整后端能力，且不包含 Java runtime 依赖
- [ ] Windows executable、启动行为和自动更新流程可用
- [ ] macOS application bundle、系统托盘、启动行为、DMG 流程可用
- [ ] Linux launcher、更新和现有服务运行方式可用
- [ ] 跨平台构建使用交叉编译，桌面托盘、安装包和 DMG 在对应原生 CI runner 上完成 smoke test
- [ ] Go-only 部署通过全部 HTTP contract、主链路端到端、外部入口、下载器、通知和 SQLite 测试
- [ ] 关闭 Java 后，现有 UI 无需修改，所有当前外部接口仍然可用
- [ ] Java 过渡后端、Gateway fallback、双写/双任务迁移逻辑和 Java 构建依赖可以删除
- [ ] 发布文档明确数据备份、回滚边界、平台产物和 Java 移除后的运行要求
