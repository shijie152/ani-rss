# 02 — downloader 能力探测下沉为 capability adapter

**What to build:** 下载器是否支持列文件、重命名、设置优先级这些能力探测，从 route shell 里的运行时类型断言变成一个下载器侧的能力适配器；完成管线的重命名/优先级分支行为不变。

**Blocked by:** None — can start immediately

**Status:** done

- [x] 路由层不再出现针对下载器的运行时能力断言（`downloader.Capabilities(adapter)` 提供 seam，返回 adapter 原生 `TorrentFile`）。`completion` 与 `downloader` 是两个 module、互不依赖，跨 seam 时仍需一次显式逐字段转换——那是模块边界的代价，不是冗余搬运层。
- [x] 支持/不支持某能力的下载器，完成管线走对应分支（downloader 侧两条分支都有测试）
- [x] 现有完成管线测试与 API parity 测试全绿（`bash scripts/check.sh` 通过）

## Test plan

行为测试覆盖「能力存在」「能力缺失」两条分支 + 完成管线既有测试回归 + `bash scripts/check.sh`。
