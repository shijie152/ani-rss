# 05: 深化 scheduler module：集中 cadence 与 lifecycle

**What to build:** 保持 Go 当前 RSS 刷新和资源源站预热行为不变，让 scheduler module 统一管理 domain ownership、首次执行、周期 cadence、后台 worker、context cancellation 和关闭生命周期；App 只组装 RSS refresh 与 source prewarm 两类 job。

**Blocked by:** 01: 深化资源源站 module：稳定 facade、adapter 与缓存策略

**Status:** ready-for-human

- [x] Go 拥有 RSS domain 时继续按现有配置刷新订阅，未拥有时不启动 RSS job
- [x] Go 拥有 sources domain 时启动一次资源源站预热，并按固定周期触发后续检查
- [x] RSS refresh 与 source prewarm 相互独立；一个上游阻塞或失败不拖住另一个 job
- [x] context cancellation 和 App.Close 能等待后台 worker 结束，不遗留 goroutine 或重复任务
- [x] ownership 组合、首次执行、周期触发、任务重叠和关闭行为可通过可控 clock/executor 验证
- [x] scheduler module 不改变 UI、HTTP interface 或 RSS 业务结果
