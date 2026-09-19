# 03: 提交前去重与并发上限（fileExist / downloadCount）

**What to build:** 在 RSS 提交链路透出 Java `downloadAni` 的两个提交前判定：`fileExist` 开启时扫描下载目录，若已存在同季同集视频则视为已下载（并落 torrent 缓存标记避免重复扫描）；`downloadCount > 0` 时统计未完成任务数，达到上限即跳过后续提交。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] `fileExist` 且 `rename` 开启时：提交前对候选资源计算 `reName`，扫描 `DownloadPath` 目录，命中同季同集（OVA 命中任意视频）即跳过并记录 `本地已存在`
- [x] 命中本地已存在时调用 `SaveResourceCache` 等价逻辑保存 torrent 标记，使后续轮次 O(1) 短路，与 Java `saveTorrent` 一致
- [x] `downloadCount > 0` 时统计下载器未完成任务数作为基数；每成功提交一个主资源（非 `.5` 集）计数递增，达到上限即停止本轮提交
- [x] `.5` 集、备用资源不计入 `currentDownloadCount`，与 Java 口径一致
- [x] 已存在于下载器任务（hash/name）或 history 的资源仍按 `PlanSubmission` 既有逻辑去重，新判定叠加在其后
- [x] 判定在提交侧而非 intake/preview 侧生效，保证 preview 仍展示完整匹配结果
- [x] 测试覆盖：本地已有同集文件→不重复提交；达到 downloadCount→超额资源不提交；`.5` 集不占名额
