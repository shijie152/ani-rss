# 12 — 删除一行转发面（contract）

**What to build:** 宽接口与它的一行转发实现删除，调用方只依赖按用途的窄接口。

**Blocked by:** 11 — source 按用途拆窄接口（expand）

**Status:** done

- [x] 宽接口与其转发实现从代码中消失（`source.Discovery` 删除，只剩三个用途接口）
- [x] 无调用方仍引用宽接口（handler 与 MCP 全部按用途取用）
- [x] 全部 handler 与 parity 测试全绿（`check.sh`；期间抓到一处真回归：后台解析误用 `r.Context()`）

## Test plan

全量 Go 测试 + API parity 回归 + `bash scripts/check.sh`。
