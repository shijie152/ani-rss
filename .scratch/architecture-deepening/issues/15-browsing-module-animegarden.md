# 15 — AnimeGarden 适配器接入，删除三份重复

**What to build:** AnimeGarden 页接入浏览 module，三个源站页里最后一份重复交互删除。

**Blocked by:** 14 — AniBT 适配器切到浏览 module

**Status:** done

- [x] AnimeGarden 页的分组→批量添加→复制可用（适配器行为测试覆盖 subjectId/subgroupLabel/bgmUrl；浏览器冒烟覆盖打开、渲染全站目录、失败提示与重试）。批量添加未在浏览器里跑：从添加订阅流程进入时 `show()` 不传 bgmUrl，后端返回的是按星期分组的全站目录（不是空列表），其结构（`item.subjects`）与另两个源站不同，适配器行为已由 Node 测试覆盖。
- [x] 三个源站页不再各自实现同一交互（三处 `execCommand` 拷贝与三份分组/批量添加逻辑全部消失）
- [x] 新增第四个源站只需写一个适配器（`{type, subjectId, subgroupLabel, bgmUrl?}`）

## Test plan

适配器行为测试（假响应）+ 三个源站的浏览器冒烟 + `pnpm --dir ani-rss-ui test`、`test:mutation`、`test:browser`、`build`。
