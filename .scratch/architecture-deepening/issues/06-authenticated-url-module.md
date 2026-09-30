# 06 — 鉴权 URL 装配 module

**What to build:** 需要携带凭据的下载/资源 URL 由一处装配产出，调用方只给路径与参数；支持会话 token 与 API key 两种凭据形状。

**Blocked by:** None — can start immediately

**Status:** done

- [x] 会话 token 与 API key 两种形状都由该 module 产出（`sessionUrl` / `apiKeyUrl`）
- [x] 现有下载/导出/日志/图片代理入口行为不变（5 处调用点改走 module，`toApiUrl` 已删）
- [x] 契约测试改为断言 module 输出，不再扫源码文本（旧的 `findRawDynamicQueryParams` 退役，保留一条更窄的「不再手拼 s=/api-key=」守卫）

## Test plan

行为测试覆盖两种凭据形状与参数合并 + 原扫源码的契约测试退役 + `pnpm --dir ani-rss-ui test`、`build`。
