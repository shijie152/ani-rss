# 04: 缺集检测与 OMIT 通知

**What to build:** 在 RSS 刷新成功匹配资源后，复用 `omitEpisodes` 的缺失集数计算，按 Java `ItemsUtil.omit` 语义发 `OMIT` 通知：`omit` 全局与 `ani.Omit` 订阅级都开启、非 OVA、缺失数 <=10 时，逐集 24h 去重后合并发送。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] `omit` 全局开启且 `ani.Omit` 且非 OVA 时，在刷新成功拿到匹配资源后计算缺失集数列表（与 `PreviewResult` 的 `omitList` 同一口径）
- [x] 缺失集数为空或 >10 时不发通知（>10 视为误判）
- [x] 每集生成 `缺少集数 {title} S{seasonFormat}E{episodeFormat}` 文本，按 `omit:{id}:ep-{n}` 键 24h 去重
- [x] 去重后非空则合并为一条 `OMIT` 事件经 dispatcher 发送，受事件过滤约束
- [x] 缺集判定只读，不影响提交与进度更新
- [x] 测试覆盖：缺集→发一次 OMIT；同日重复刷新→去重不重复发；>10 缺集→不发
