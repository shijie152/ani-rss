# 03 — 抽出 cache[T]，先迁 source

**What to build:** 资源源站的缓存（LRU + stale-while-revalidate + singleflight + 后台刷新）由一份通用实现提供，source 只负责 key 构造与时间策略；对上游调用的可见行为不变。

**Blocked by:** None — can start immediately

**Status:** done

- [x] 通用缓存 module 提供命中/未命中/过期/并发合并四种行为（`internal/cache` 82.2% 覆盖）
- [x] source 侧只保留 key 与 policy，缓存语义不再在 source 内实现（`source/cache.go` 已删除）
- [x] source 既有缓存测试在新 seam 上通过（并抓到两个真实回归：后台刷新接线丢失、nil 解引用）

## Test plan

对通用缓存 seam 的行为测试（fresh / stale / miss / singleflight / 驱逐）+ source 回归 + `bash scripts/check.sh`。
