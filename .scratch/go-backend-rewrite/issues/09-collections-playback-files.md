# 09: 合集、播放与媒体文件接口

**What to build:** 让用户通过现有 UI 预览和下载合集、查看播放列表、获取字幕并安全访问媒体文件。

**Blocked by:** 06: 刮削、媒体整理与重命名；07: Transmission、Aria2 与 OpenList 下载器适配

**Status:** ready-for-human

- [x] 合集字幕组查询、内容预览和合集下载接口可用
- [x] 合集中的多集资源能正确匹配下载器文件、调整文件优先级并完成批量重命名
- [x] 播放列表能列出已整理的视频及其关联字幕和媒体信息
- [x] 字幕查询能正确返回内封/外置字幕信息，并支持当前 UI 的文件名参数格式
- [x] 授权媒体文件可以以正确的 Content-Type、缓存头和流式响应返回
- [x] Torrent 缓存查询和删除接口可用
- [x] 未授权访问、路径遍历、越权路径、非法文件类型和不存在文件均被拒绝或返回稳定错误
- [x] 使用临时媒体库和 fake 下载器完成合集、播放、字幕和文件访问黑盒测试

## Comments

- Added UI-compatible collection routes backed by a strict bencode metainfo parser. Preview applies the existing include/exclude rules and rename template; start submits a paused qBittorrent task, matches files by basename and size, disables unmatched files, renames matched files, and starts the task.
- Hardened media file authorization with canonical path checks, symlink escape prevention, Range streaming, MIME types, and Java-compatible small-asset cache headers.
- RSS resources now retain per-subscription torrent caches (magnet/URI as `.txt`, HTTP torrent as `.torrent`); preview reports cache state and `deleteTorrent` removes only the selected subscription's hashes.

## Acceptance evidence

`go test ./...`, `go test -race ./...`, and `go vet ./...` pass. `internal/torrent/metainfo_test.go` covers single/multi-file parsing, exact info-hash calculation, and unsafe paths. `internal/collection/service_test.go` and `internal/backend/app_test.go` cover collection preview/download, fake qBittorrent file reconciliation, playlist/file Range streaming, MIME/cache headers, authentication, illegal types, path traversal, and symlink escape. `internal/rss/cache_test.go` covers isolated cache creation, HTTP torrent persistence, lookup, and deletion.
