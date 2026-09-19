# 01: 下载完成轮询与媒体整理管线（RenameTask 等价物）

**What to build:** 新增一个由 scheduler 驱动的下载完成处理任务，对应 Java `RenameTask`：按 `renameSleepSeconds` 周期轮询下载器任务列表，对每个已完成且未标记完成的任务，按保存路径反查订阅、判定字幕组、调用 `media.Service.Scrape` 完成刮削+重命名+移动+完结迁移，然后给任务打完成标签防止重复处理，并按 `delete`/`deleteStandbyRSSOnly` 规则删除做种完成的任务。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] scheduler 注册一个 `media`/`rename` 域的周期 job，间隔取 `renameSleepSeconds`（缺省/非法值回退为 10s），与 `rss`/`sources` 一样受 ownership 锁保护
- [x] 轮询当前已配置下载器的任务列表；对每个 `finished()` 的任务执行完成处理；未完成任务跳过
- [x] 用保存路径匹配 `DownloadPath` 反查订阅（`findAniByDownloadPath` 等价）；查不到只记 debug，不报错中断整轮
- [x] 依据任务 tags 反推字幕组：优先取 `StandbyRSSList` 标签命中的值，否则用订阅 `Subgroup`，兜底 `未知字幕组`
- [x] 已含完成标签的任务直接跳过；处理成功后调用 `addTags` 打标签，保证幂等
- [x] `scrape` 配置开启时调用 `media.Service.Scrape(ani, false)`；刮削失败只记日志，不阻断后续通知与删除
- [x] `delete` 开启时按 Java 语义删除任务：`deleteStandbyRSSOnly` 时仅删备用/非主字幕组任务，否则删除已做种完成的任务
- [x] 一轮内单个任务处理失败不影响其余任务；整轮异常被捕获并记录，不使 scheduler 退出
- [x] 提供 fake downloader 的确定性测试：完成任务→订阅反查→Scrape 被调用→打完成标签→二次轮询跳过
