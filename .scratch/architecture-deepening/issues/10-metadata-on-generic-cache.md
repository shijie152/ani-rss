# 10 — metadata 迁到 cache[T]，合并两份缓存测试

**What to build:** 元数据服务复用通用缓存实现，只保留 key 与策略；重复的缓存实现删除，两份缓存测试合并为一份。

**Blocked by:** 03 — 抽出 cache[T]，先迁 source

**Status:** done

- [x] 元数据缓存不再有自己的 LRU/stale/singleflight 实现（163 行 → 90 行，只留 policy 与拷贝语义）
- [x] 两个上游的缓存行为一致（共用 `internal/cache`）
- [x] 缓存行为测试只剩一份（`internal/cache/cache_test.go`；metadata 侧保留 policy 与不可变性测试）

## Test plan

通用缓存 seam 的行为测试（沿用 03 的用例）+ 元数据回归 + `bash scripts/check.sh`。
