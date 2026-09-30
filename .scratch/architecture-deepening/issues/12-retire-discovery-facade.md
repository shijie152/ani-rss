# 12 — 删除一行转发面（contract）

**What to build:** 宽接口 `Discovery` 删除，调用方只依赖按用途的窄接口。`Client` 保留：它是三个 adapter 的组装点（`BangumiMetadata` 需要 mikan + bgm 组合），其 14 个方法恰好是三个窄接口的并集加一个内部方法，不是冗余转发层。

**Blocked by:** 11 — source 按用途拆窄接口（expand）

**Status:** done

- [x] 宽接口从代码中消失（`source.Discovery` 删除，只剩三个用途接口；`Client` 作为 adapter 组装点保留）
- [x] 无调用方仍引用宽接口（handler 与 MCP 全部按用途取用）
- [x] 全部 handler 与 parity 测试全绿（`check.sh`；期间抓到一处真回归：后台解析误用 `r.Context()`）

## Test plan

全量 Go 测试 + API parity 回归 + `bash scripts/check.sh`。
