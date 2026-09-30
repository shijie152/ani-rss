# 15 — AnimeGarden 适配器接入，删除三份重复

**What to build:** AnimeGarden 页接入浏览 module，三个源站页里最后一份重复交互删除。

**Blocked by:** 14 — AniBT 适配器切到浏览 module

**Status:** done

- [x] AnimeGarden 页搜索→分组→批量添加→复制全流程可用（新增 `tests/browser/source-browsing.spec.mjs`：三个源站页在真实浏览器里各打开一次）
- [x] 三个源站页不再各自实现同一交互（三处 `execCommand` 拷贝与三份分组/批量添加逻辑全部消失）
- [x] 新增第四个源站只需写一个适配器（`{type, subjectId, subgroupLabel, bgmUrl?}`）

## Test plan

适配器行为测试（假响应）+ 三个源站的浏览器冒烟 + `pnpm --dir ani-rss-ui test`、`test:mutation`、`test:browser`、`build`。
