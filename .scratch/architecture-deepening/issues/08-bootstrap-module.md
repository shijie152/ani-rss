# 08 — 启动与关闭收敛成 bootstrap module

**What to build:** 进程级装配、领域归属检查、启动与关闭顺序收进一个 module，接口只有「起服务」与「关闭」；`main` 退化成一行调用。

**Blocked by:** 07 — 装配 seam：迁移 media/metadata/source 批次，删除三个旧工厂

**Status:** done

- [x] 只有 owned domain 的定时任务被启动（`RunSchedulers(ctx)` 仍由 app 按 OwnedDomains 决定，bootstrap 只传 options）
- [x] 关闭顺序由该 module 决定，不散落在调用方（gateway → scheduler → backend，全在 `Run` 里）
- [x] `main` 只负责读取选项并调用它（134 → 104 行，不再自己拼 server/scheduler/exec）
- [x] 现有启动/调度相关测试全绿（新增 `bootstrap_test.go` 两条 + `check.sh`）

## Test plan

行为测试（给定 domain 集合只跑 owned job、关闭顺序）+ 既有调度测试回归 + `bash scripts/check.sh`。
