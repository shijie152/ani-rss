# 11: MCP 与 Swagger/OpenAPI

**What to build:** 让 MCP 客户端和 API 使用者继续访问当前 MCP 工具与 Swagger/OpenAPI 文档，并与 Go 实际提供的 HTTP 能力保持一致。

**Blocked by:** 03: 订阅管理与 JSON 数据 Store；04: 资源源站发现与选择；05: RSS 刷新到 qBittorrent 下载主链路；06: 刮削、媒体整理与重命名；07: Transmission、Aria2 与 OpenList 下载器适配；08: 通知、Emby 与事件后处理；09: 合集、播放与媒体文件接口；10: 管理、备份、上传与外部 HTTP 接口

**Status:** ready-for-agent

- [ ] 当前 MCP endpoint 可被支持 streamable HTTP 的客户端建立连接
- [ ] 当前 MCP 工具名称、输入结构、输出结构和错误行为保持可用
- [ ] MCP 工具调用遵守与 UI/API 相同的认证和权限边界
- [ ] Swagger UI 和 OpenAPI 文档能够描述 Go 实际提供的公开接口
- [ ] 文档中的路径、方法、参数、请求体、响应体和鉴权信息与 HTTP contract tests 一致
- [ ] MCP fake client 和 OpenAPI validation tests 能覆盖正常调用、无权限调用和外部服务失败
