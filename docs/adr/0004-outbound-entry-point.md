# 第三方调用统一走 outbound module

所有对外部服务的调用都经由 `go-backend/internal/outbound`。这个 module 拥有代理选择、每次尝试的超时、请求形状（方法、头、体、User-Agent）、重试策略、响应体积上限和出站日志；调用方描述一个请求并拿到结果，拿不到裸的 `*http.Client`。

## Context

`docs/migration/go-backend-rewrite-spec.md` 要求「代理选择、超时与重试策略不要散落在领域逻辑里」，但迁移完成后实际散落了四份几乎同形的循环：

- `source` runtime 的 retry + UA + 16 MiB 上限
- `rss` Fetch 的 retry + UA + 32 MiB 上限
- `metadata` 的 retry + UA + 16 MiB 上限
- `downloader` request 的 3 次尝试 + 5xx 重试 + 32 MiB 上限

此外有 11 处 `&http.Client{Timeout: 20 * time.Second}` 兜底散在 8 个包里，`components.HTTPClient` 与 `httpclientForConfig` 是同一个表达式起的两个名字，另有 5 处直接调用工厂。

代价是两条真实缺口：

- **通知与自动更新检查完全没接配置。** `notification.New(..., nil, ...)` 与未设 `HTTP` 的 `update.Client` 各自在包内自建 client，用户在设置里配的代理与超时对它们静默失效——这正是「配了代理但通知还是发不出去」这类问题的来源。
- **策略没有 locality。** 改一次重试次数、UA 或体积上限要动四个包。

## Considered Options

- **只补通知与更新的接线。** 最小改动，但四份重复循环仍在，下次加一条外呼链路还会漏。
- **保留 client 工厂，只收敛调用点。** 工厂的接口是一个裸 `*http.Client`，是浅 module：调用方拿到它就能各自长回重试循环，收敛不持久。
- **把浅工厂换成深 module（本决定）。** 策略、请求形状与观测收进一个实现，接口小到只剩「描述请求、拿结果」。

## Consequences

调用方不再持有裸 client，因此无法再长出第 12 处兜底；`outbound.Client` 的 `Call` 是唯一策略入口。错误文案按调用点保留（`Call.StatusPrefix`），`source returned HTTP` 这条被 MCP 测试断言的片段与 Java 版的中文文案（`RSS服务返回`、`资源服务返回`）都没有改。

**明确的行为变化：**

1. 通知与自动更新检查现在受 `proxy` 与 `rssTimeout` 约束（此前固定 20 秒 / 5 秒且不走代理）。这是修复，也是本 ADR 的起因。
2. `metadata` 不再对「2xx 但 JSON 解析失败」重试（此前会重试 3 次）。重复请求同一个坏响应没有收益。
3. `downloader` 的重试之间新增 50ms / 100ms 退避（此前立即重试）；qBittorrent 的响应体积上限统一为 8 MiB（此前 login 1 MiB、preferences 4 MiB、其余 8 MiB）。
4. 所有外呼带上 `ani-rss-go`（此前通知、下载器与图片下载走 Go 默认 UA）。

**观测：** 成功调用在 DEBUG 级记录 host 与 path（去掉 query，避免 `api_key` 之类的凭据进日志文件与日志页），失败与 5xx 始终记 WARN。设置页的 `debug` 开关此前在 Go 后端未被使用，现在由它控制成功记录。

**长生命周期组件用 `Watch`。** 通知与更新检查在启动时装配一次，因此它们的 client 每次调用都从当前配置快照解析策略，transport 只在代理设置变化时重建；用户保存代理设置后无需重启（重启在关于页是手动动作）。

**缺 client 的规则按签名机械决定：** 能返回错误的入口（`notification.New`、`update.Client`、downloader 的 request、`rss.Fetch`、`SaveResourceCache`）把缺失当作配置错误报出；无法返回错误的构造函数（`source.New`、`metadata.New`、`media.New`）保留 nil 表示「共享默认策略」的可选依赖契约，应用侧始终传入按当前配置构建的 client。

`internal/httpclient` 被删除，它的 `ShouldProxy` 与测试并入 outbound。`components.Outbound` 成为装配 seam 上取 client 的唯一入口。
