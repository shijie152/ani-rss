# 01 — 装配 seam：引入 components module，先迁 coordinator 批次

**What to build:** 从当前配置快照装配协调器这件事，从一个私有工厂函数变成一个可被测试直接调用的 module；handler 行为与对外契约完全不变，coordinator 相关调用点先走新入口。

**Blocked by:** None — can start immediately

**Status:** done

- [x] 装配入口只有一个，且不依赖 HTTP 路由即可触达（`App.Components().Coordinator()`）
- [x] 配置快照变化后，新装配出来的协调器使用新值（`downloadRetry` 3 → 7 实测）
- [x] 原有 API parity 测试全绿，未改任何对外响应
- [x] coordinator 相关调用点全部走新入口（9 处），旧工厂函数已删除

## Test plan

行为测试（直接对新 seam 断言装配结果随配置变化）+ 现有 API parity/契约测试回归 + `bash scripts/check.sh`。
