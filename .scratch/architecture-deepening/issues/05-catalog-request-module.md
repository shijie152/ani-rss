# 05 — 目录请求 module（缓存 + stale-while-revalidate + 序列守卫）

**What to build:** 「带缓存与后台刷新的目录请求」成为一个可注入 fetch 与 storage 的 module；命中、过期后台刷新、失败回退缓存、过期序列丢弃四条决策只写一处，季度目录与源站搜索都走它。

**Blocked by:** None — can start immediately

**Status:** done

- [x] 缓存命中直接返回，不发网络请求
- [x] 缓存过期时先返回旧数据并在后台刷新
- [x] 请求失败时回退到缓存数据
- [x] 晚到的过期响应被丢弃，不覆盖新结果
- [x] 时间策略只有一处定义（`createCatalogRequest({ttl})`），季度视图两个 loader 不再各写一份决策（视图 -60 行）

## Test plan

用假 fetch + 假 storage 覆盖四条分支的行为测试 + 季度页浏览器冒烟回归 + `pnpm --dir ani-rss-ui test`、`test:mutation`、`test:browser`、`build`。
