# 05: 摸鱼检测与 PROCRASTINATING 通知

**What to build:** 在 RSS 刷新匹配资源后，按 Java `ItemsUtil.procrastinating` 语义做摸鱼检测：`procrastinating` 全局与 `ani.Procrastinating` 都开启时，取（可选仅主 RSS）资源最新 pubDate，距今超过 `procrastinatingDay` 天即发一次 `PROCRASTINATING` 通知，按订阅 24h 去重。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] `procrastinating` 全局开启且 `ani.Procrastinating` 时参与检测；`procrastinatingMasterOnly` 开启时只用 `Master` 资源
- [x] 取资源最新 `PublishedAt`，为 null 或时间在未来则跳过；距今天数 >= `procrastinatingDay` 才触发
- [x] 通知文本 `检测到{title}, 已摸鱼{day}天`，按 `procrastinating:{id}` 键 24h 去重
- [x] 经 dispatcher 发 `PROCRASTINATING` 事件，受事件过滤约束
- [x] 只读检测，不影响匹配、提交与进度
- [x] 测试覆盖：超期→发一次；同日重复→去重；未超期/无 pubDate→不发
