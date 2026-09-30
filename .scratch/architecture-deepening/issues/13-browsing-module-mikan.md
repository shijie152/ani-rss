# 13 — 浏览 module + Mikan 适配器

**What to build:** 搜索、字幕组分组、RSS 匹配弹窗、批量添加、复制 RSS 这套交互只实现一遍；Mikan 作为第一个适配器接入，源站页端到端可用。

**Blocked by:** 05 — 目录请求 module（缓存 + stale-while-revalidate + 序列守卫）

**Status:** done

- [x] Mikan 页的搜索→分组→批量添加→复制全流程可用（浏览器冒烟 + build 通过）
- [x] 交互逻辑不在视图里重复实现（批量添加走 `submitBatch`，视图只给 adapter 与三个回调）
- [x] 适配器只需声明端点与字段映射即可接入（`mikanAdapter`：subjectId + subgroupLabel + type）
- [x] 用假响应即可断言「搜索→分组→批量添加」行为（`sourceBrowsing.test.mjs` 5 条）

## Test plan

适配器行为测试（假响应）+ 季度页/源站页浏览器冒烟 + `pnpm --dir ani-rss-ui test`、`test:mutation`、`test:browser`、`build`。
