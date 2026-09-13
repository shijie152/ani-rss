# 08: 通知、Emby 与事件后处理

**What to build:** 让发现资源、开始下载、下载完成、洗版和媒体整理等通知事件触发当前配置的通知与后处理动作。

**Blocked by:** 05: RSS 刷新到 qBittorrent 下载主链路；06: 刮削、媒体整理与重命名

**Status:** ready-for-human

- [x] Telegram、Bark、邮件、Webhook、Shell、文件移动、OpenList 上传和 Emby 刷新通知均可配置和测试
- [x] 通知事件能携带番剧、资源、季集、字幕组、路径、元数据和处理结果
- [x] 通知状态过滤、模板渲染、Markdown/HTML 等格式选择和延迟行为符合当前 UI 预期
- [x] HTTP、SMTP 和 Shell 执行失败具备超时、重试、失败记录和可诊断日志
- [x] 文件移动、OpenList 上传和本地文件删除行为遵守显式配置，失败时不会静默丢失媒体
- [x] Emby refresh 请求和 Emby Webhook 触发的现有处理逻辑可用
- [x] fake HTTP、SMTP 和 process boundary 测试证明同一事件不会重复发送或重复执行后处理
- [x] 用户可以在现有通知配置页面完成新增、编辑、测试和删除

## Comments

- Added a Go notification dispatcher with status filtering, ordered configs, template rendering, per-channel retries, diagnostics, and process-lifetime event deduplication.
- Added UI-compatible `testNotification`, `newNotification`, `getTgUpdates`, `getEmbyViews`, and `embyWebHook` routes without changing Vue source.
- RSS submission emits `DOWNLOAD_START`; media processing emits `DOWNLOAD_END`. Emby playback/mark events update Bangumi episode collection state when a subscription matches.
- Implemented Telegram, Bark, ServerChan, WebHook, SMTP (plain/TLS), Shell timeout, local file move, OpenList upload, and Emby refresh actions with explicit delete/copy behavior.

## Acceptance evidence

`go test ./...`, `go test -race ./...`, and `go vet ./...` pass. `go-backend/internal/notification/service_test.go` covers HTTP retry/status filtering/deduplication, Telegram chat deduplication, Emby refresh, fake SMTP, shell success/timeout, local move, and OpenList upload/delete behavior. `go-backend/internal/backend/app_test.go` covers UI-compatible notification/Emby routes and asynchronous Emby-to-Bangumi update behavior.
