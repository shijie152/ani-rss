# 07 — 装配 seam：迁移 media/metadata/source 批次，删除三个旧工厂

**What to build:** 装配入口成为唯一来源，媒体服务、元数据客户端、资源源站客户端都从它取；三个私有工厂函数删除，handler 只消费。

**Blocked by:** 01 — 装配 seam：引入 components module，先迁 coordinator 批次

**Status:** done

- [x] 路由层不再出现媒体服务/元数据客户端/源站客户端的重复装配（三个旧工厂已删除）
- [x] 所有调用点走同一装配入口（25 处 → `App.Components()`）
- [x] 装配逻辑可在不经过 HTTP 的情况下被测试断言（`assembly_test.go` 直接断言装配结果）
- [x] API parity 测试全绿，对外响应无变化（`bash scripts/check.sh` 通过）

## Test plan

对新 seam 的装配行为测试 + API parity/契约测试回归 + `bash scripts/check.sh`。
