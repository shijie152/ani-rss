# 12: SQLite 最终 Store 与数据切换

**What to build:** 把 Go 的配置、订阅、缓存、下载任务、通知配置和运行状态从 JSON Store 切换到 SQLite，并提供可靠的导入、导出和备份恢复能力。

**Blocked by:** 03: 订阅管理与 JSON 数据 Store；05: RSS 刷新到 qBittorrent 下载主链路；06: 刮削、媒体整理与重命名；07: Transmission、Aria2 与 OpenList 下载器适配；08: 通知、Emby 与事件后处理；09: 合集、播放与媒体文件接口；10: 管理、备份、上传与外部 HTTP 接口；11: MCP 与 Swagger/OpenAPI

**Status:** ready-for-human

- [x] SQLite 成为 Go 运行时的唯一业务数据源，业务模块不再直接读写 JSON
- [x] 配置、订阅、资源历史/缓存、下载任务、通知配置和必要运行状态可以持久化并在重启后恢复
- [x] JSON 导入能验证输入、报告缺失/冲突/非法数据，并保留当前 UI 所需的用户可见数据
- [x] JSON 导出能生成可再次导入的用户数据，ZIP 备份能恢复完整应用状态
- [x] 事务、并发访问、任务状态恢复和异常中断不会造成部分写入或数据损坏
- [x] 导入前后 UI、RSS 主链路、四种下载器、媒体整理和通知行为保持正确
- [x] 存储测试验证外部行为，不把不必要的私有表结构作为测试契约
- [x] 迁移完成后可关闭 JSON 写入和 Java 数据所有权，不产生双写

实现说明：Go App 默认使用 `ani-rss.sqlite`；首次启动会将旧 `config.v2.json`、`ani.v2.json`、`resources.v2.json` 和 `tasks.v2.json` 导入 SQLite，之后业务读写不再触碰这些 JSON 文件。配置、通知配置和运行配置保存在配置状态中，资源历史与下载任务快照分别保存为事务状态。导出仍生成可导入的 JSON 文档，ZIP 备份同时保存这些文档及文件/种子目录；恢复前执行输入校验，并以 SQLite 事务替换逻辑状态。
