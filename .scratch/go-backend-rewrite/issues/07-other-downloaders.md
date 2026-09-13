# 07: Transmission、Aria2 与 OpenList 下载器适配

**What to build:** 让用户无需修改 UI 即可切换并使用 Transmission、Aria2 或 OpenList，完成与 qBittorrent 等价的下载任务生命周期管理。

**Blocked by:** 05: RSS 刷新到 qBittorrent 下载主链路

**Status:** ready-for-human

- [x] 三种下载器都实现统一的登录测试、资源提交、任务列表、状态查询和错误映射
- [x] Transmission 的 session id、RPC 请求、删除、重命名、标签和保存路径行为可用
- [x] Aria2 的 JSON-RPC、磁力/种子提交、状态、删除、保存路径和 Tracker 更新行为可用
- [x] OpenList 的离线下载、任务轮询、重试、远端文件检查、重命名和移动行为可用
- [x] 下载器切换只改变配置，不要求修改 UI 或 RSS 匹配逻辑
- [x] 每种下载器都有独立 fake server，覆盖认证失败、超时、重试、异常状态和部分成功
- [x] 下载器返回的状态都能映射到统一的下载任务生命周期
- [x] 真实下载器冒烟测试验证主链路不会因 fake server 与实际协议差异而失效

## Comments

- Go downloader adapters now share a protocol-independent lifecycle boundary and factory selected by the existing `downloadToolType` configuration field.
- Transmission handles the 409 session-id challenge and RPC lifecycle operations; Aria2 handles JSON-RPC token calls, magnet/torrent submission, status mapping, and tracker updates; OpenList handles offline task polling, retry, timeout, remote file discovery, and filesystem operations.
- Acceptance coverage lives in `go-backend/internal/downloader/other_downloaders_test.go`, with independent fake HTTP servers for protocol success, partial results, authentication failures, transient failures, permanent failures, and timeout cancellation.
- `go-backend/internal/downloader/real_smoke_test.go` provides non-mutating opt-in smoke checks against real services through `ANI_RSS_TRANSMISSION_*`, `ANI_RSS_ARIA2_*`, and `ANI_RSS_OPENLIST_*` environment variables; it is skipped when no real downloader is configured in the test environment.

## Acceptance evidence

`go test ./...`, `go test -race ./...`, and `go vet ./...` pass for the Go backend. Fake protocol tests cover session negotiation, token/basic authentication, magnet and remote torrent submission, lifecycle operations, partial task results, status normalization, retry, permanent failure, and context timeout. The real-service smoke test is available but was skipped locally because Transmission, Aria2, and OpenList services are not installed or configured.
