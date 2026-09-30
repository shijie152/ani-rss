# 04 — 端点描述表：端点自带 path/method/readOnly

**What to build:** 前端每个端点的「是否只读」由端点自己声明，请求层从声明派生去重决策，而不是查一份外部手写名单；调用方用法与对外 API 契约不变。

**Blocked by:** None — can start immediately

**Status:** done

- [x] 新增一个只读端点只需改一处，去重自动生效（`endpoints.js` 是唯一真相源）
- [x] 遍历端点定义即可断言每个端点的去重决策（`endpoints.test.mjs` 遍历全表）
- [x] 查询串参与去重 key（不同搜索词不共用同一请求）
- [x] 把去重决策改错会被现有变异测试杀死（新增 `endpoint-readonly-table-drift` 变异，10/10 被杀）

## Test plan

行为测试（遍历端点断言去重决策、不同查询串不合并）+ 变异测试新增「名单/描述漂移」用例 + `pnpm --dir ani-rss-ui test`、`test:mutation`、`build`。
