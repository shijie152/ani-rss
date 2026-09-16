# 01: 深化资源源站 module：稳定 facade、adapter 与缓存策略

**What to build:** 保持现有 UI 和 HTTP interface 不变，让 Mikan、AniBT、AnimeGarden 与 Bangumi 的目录、搜索和详情访问通过一个稳定的资源发现 facade；源站协议、解析、重试、缓存生命周期和 stale refresh 收进 deep implementation，本地订阅状态在缓存之后重新叠加。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] Mikan、AniBT、AnimeGarden 与 Bangumi 的现有路由、参数和返回字段保持不变
- [x] 各源站 adapter 的协议差异集中在资源发现 module 内，caller 不再处理源站-specific 解析细节
- [x] 目录、搜索、详情和封面数据按既定 freshness/staleness 策略复用缓存；并发同 key 请求只触发一次上游访问
- [x] stale 数据可先返回并异步刷新；上游失败时保留可用 stale 数据并避免污染缓存
- [x] `exists` 等本地派生字段每次根据当前订阅重新计算，不被长期缓存冻结
- [x] 通过 interface 验证缓存命中、过期、并发合并、刷新失败和结果不可变性
