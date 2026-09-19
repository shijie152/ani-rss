Status: done

# 下载完成后处理管线补全

## Problem Statement

Go 重写已覆盖全部 71 个 HTTP 路由契约，差分/fuzz 测试全绿。但对照 Java 基线（7c81be9e）发现：Java 的 `RenameTask` 是一个独立的周期性任务（`renameSleepSeconds`，默认 10s），负责轮询下载器、对完成的任务执行刮削+重命名+移动、发下载完成通知、完结停订、做种后删除。Go 的 `RunSchedulers` 只注册了 `rss` 和 `sources` 两个 job，没有任何轮询下载器处理完成任务的循环。

结果是：资源能提交给下载器，但下载完成后不会自动整理进媒体库、不会发完成/完结通知、不会自动停订。此外若干提交前的行为也未移植：`fileExist` 本地已存在跳过、`downloadCount` 并发下载上限、`omit` 缺集通知、`procrastinating` 摸鱼检测。这些副作用都不在 HTTP 边界上，所以契约测试探针覆盖不到，需要专门补。

## Solution

新增一个独立的下载完成处理任务（对应 Java `RenameTask`），由 scheduler 按 `renameSleepSeconds` 周期驱动：轮询下载器任务 → 对每个已完成的任务找到对应订阅 → 刮削/重命名/移动（复用现有 `media.Service`）→ 打完成标签防重 → 发 `DOWNLOAD_END` 通知 → 完结迁移/自动停订（`autoDisabled`）→ 可选做种后删除。提交侧补上 `fileExist` 本地去重和 `downloadCount` 上限；RSS 刷新链路补上 `omit` 缺集通知与 `procrastinating` 摸鱼检测。全部经通知 dispatcher 走既有事件过滤与去重。

## Out of Scope

- 改变任何 HTTP 路由、参数或返回契约。
- 重写 media.Service 的刮削/重命名/NFO 逻辑（直接复用）。
- 新增 Java 没有的通知类型或下载器。

## Java baseline references

- `task/RenameTask.java` — 周期轮询 + rename + notification + delete
- `service/DownloadService.java` `downloadAni`/`notification`/`itemDownloaded`/`deleteStandbyRss`
- `util/other/ItemsUtil.java` `omit`/`omitList`/`procrastinating`
