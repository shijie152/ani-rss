# 架构深化：把浅模块做深

来源：2026-09-30 的架构审查（8 个候选，探针实测过关键数字）。目标是把浅 module 做深，换取 locality、leverage 与更可测的 seam。

## 候选清单

| # | 候选 | strength | 证据 |
|---|---|---|---|
| B1 | App 的组件工厂收成一个装配 module | Strong | `app.go` 2429 行；4 个私有工厂 24 处调用点；backend 覆盖率 61.3% vs source 78.3% / completion 80.4% |
| B2 | 按用途拆开 `source.Client` 的转发面 | Strong | 95 行 / 14 个一行转发；deletion test 指向 pass-through |
| B3 | 启动与关闭收敛成 bootstrap module | Worth exploring | `main` 与 update 处理器各有一份 restart |
| B4 | 两份缓存实现收成一个泛型 `cache[T]` | Worth exploring | `source/cache.go` 与 `metadata/cache.go` 连 `cacheEntry`/`cacheCall` 都同名 |
| F1 | 让端点自己声明 readOnly | Strong | 54 个 export vs 19 条手写名单；a5e96452 的 bug 就是这条裂口 |
| F2 | 三个源站浏览页收敛成一个浏览 module | Strong | 616/488/450 行；`batchAddition` 各 11 次 |
| F3 | stale-while-revalidate 决策搬出视图 | Worth exploring | 纯助手被抽出来了，TTL/守卫/回退仍在两个视图各一份 |
| F4 | 鉴权 URL 装配集中一处 | Speculative | `?s=` / `?api-key=` 4 处手拼，靠一条扫源码的正则兜底 |

## Top recommendation

B1：`app.go` 是最近 200 次提交改动最多的文件，装配没有 seam，35 个 handler 与 24 处调用点各自重建组件树。

## 约束

- ADR-0001：UI 对外 API 契约（路径、方法、参数、返回 JSON、认证方式）不变。
- ADR-0002：SQLite 是最终数据源，本批工作不改变存储边界。
- 领域词汇以 `CONTEXT.md` 为准；架构词汇用 module / interface / implementation / depth / seam / adapter / leverage / locality。

## 工单

见 `issues/`，编号即依赖顺序；无阻塞的是 01–06。
