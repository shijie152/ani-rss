# 11 — source 按用途拆窄接口（expand）

**What to build:** 资源源站客户端按用途暴露三组窄接口——番剧元数据、目录发现、RSS 到订阅的解析；旧的宽接口保留，调用方按用途分批迁移。

**Blocked by:** 07 — 装配 seam：迁移 media/metadata/source 批次，删除三个旧工厂

**Status:** done

- [x] 只做元数据的调用方只依赖元数据那组方法（`BangumiMetadata` 5 个方法）
- [x] 目录发现与 RSS 解析各自只依赖自己那组（`CatalogueDiscovery` 6 个 / `RSSResolver` 2 个）
- [x] 旧宽接口仍在（expand 阶段两者并存，`purpose_test.go` 断言 Client 同时满足四者）
- [x] 相关 handler 行为不变（`check.sh` 全绿）

## Test plan

按用途的 fake 实现（各只需 4–5 个方法）驱动 handler 测试 + API parity 回归 + `bash scripts/check.sh`。
