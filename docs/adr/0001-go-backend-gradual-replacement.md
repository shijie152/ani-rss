# 使用 Go 后端渐进替换 Java，并保持现有 UI 契约

ANI-RSS 将使用 Go 重建后端，最终移除 Java；迁移期间运行 Go 网关和 Java 过渡后端，已迁移的业务域由 Go 处理，未迁移的请求转发到 Java。现有 Vue UI 不修改，因此当前 UI 使用的 API 路径、HTTP 方法、参数、返回 JSON、认证方式以及 Emby、ICS、API Key、MCP 和 Swagger 等已确认的外部接口需要保持不变；迁移按业务域推进，最终在关闭 Java 后完成全链路验收。

## Considered Options

- 一次性停止 Java 并切换 Go：切换面过大，失败时回滚困难。
- 长期保留 Java 和 Go：降低短期迁移压力，但会永久增加部署、数据和任务一致性成本。
- 渐进式迁移后移除 Java：允许按业务域验证和回滚，同时保留明确的最终收敛目标。

## Consequences

迁移期间只能有一个进程拥有 RSS、重命名等定时任务，也只能有一个明确的数据写入者。Go 二进制可以交叉编译；Windows/macOS 托盘、安装包和 DMG 仍需在对应平台的 CI 环境中验证和打包。
