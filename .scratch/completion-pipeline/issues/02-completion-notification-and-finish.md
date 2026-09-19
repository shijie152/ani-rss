# 02: 下载完成通知与完结自动停订

**What to build:** 在 01 的完成处理中接入通知与完结生命周期：任务整理完成后按 Java `downloadService.notification` 语义发 `DOWNLOAD_END`（备用 RSS 加 `(备用RSS)` 前缀），并执行 `AniUtil.completed` 等价的完结判定——`autoDisabled` 且当前集数达到总集数时发 `COMPLETED` 通知并把订阅置为停用。完结目录迁移沿用 `media.Service.moveCompleted`。

**Blocked by:** 01: 下载完成轮询与媒体整理管线（RenameTask 等价物）

**Status:** ready-for-human

- [x] 完成处理成功后经 `notification.Dispatcher` 发 `DOWNLOAD_END`，文本 `{name} 下载完成`，备用 RSS 任务加 `(备用RSS) ` 前缀；受通知事件过滤与去重约束
- [x] `DOWNLOAD_END`/`COMPLETED` 状态进入既有 dispatcher 的标签/emoji/事件过滤路径，而非旁路
- [x] `autoDisabled` 开启且 `TotalEpisodeNumber > 0` 且当前已下载集数 >= `TotalEpisodeNumber` 时：发 `COMPLETED` 通知（`{title} 订阅已完结`）并将订阅 `Enable=false` 持久化
- [x] 完结判定集数口径与 Java 一致（主资源整数集计数，`.5` 集不计入 currentDownloadCount）
- [x] `moveCompleted` 完结目录迁移在 `completed` 与 `autoDisabled` 同时开启时按 `completedPathTemplate`/`CustomCompletedPathTemplate` 生效
- [x] 完结迁移/停订/通知任一失败只记日志，不阻断本轮其余任务
- [x] fake downloader + 内存通知捕获的测试：覆盖完成通知、备用前缀、完结停订与完结迁移
