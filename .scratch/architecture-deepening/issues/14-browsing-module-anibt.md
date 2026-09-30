# 14 — AniBT 适配器切到浏览 module

**What to build:** AniBT 页接入同一个浏览 module，视图里重复的那套交互删除。

**Blocked by:** 13 — 浏览 module + Mikan 适配器

**Status:** done

- [x] AniBT 页搜索→分组→批量添加→复制全流程可用（build + 浏览器冒烟通过）
- [x] 该视图不再包含重复的交互实现（`batchAddition` 55 行 → 15 行，只剩 adapter + 三个回调）
- [x] 两个源站共用同一份实现（`submitBatch`；`copy` 三份逐字重复也一并收进 `copyText`）

## Test plan

适配器行为测试（假响应）+ 浏览器冒烟 + `pnpm --dir ani-rss-ui test`、`test:browser`、`build`。
