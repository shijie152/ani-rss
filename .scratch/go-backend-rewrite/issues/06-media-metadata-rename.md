# 06: 刮削、媒体整理与重命名

**What to build:** 让下载完成的媒体经过 TMDB/Bangumi 刮削、文件识别、字幕匹配、规范重命名、NFO/封面生成和媒体库整理后，呈现给用户正确的结果。

**Blocked by:** 05: RSS 刷新到 qBittorrent 下载主链路

**Status:** ready-for-human

- [x] TMDB/Bangumi 元数据查询、标题/评分/季集信息、封面和刮削接口可用
- [x] 手动刮削、批量刮削、刷新封面和更新总集数接口可用
- [x] 视频、字幕和图片文件按当前支持的格式被识别，其他文件不被误处理
- [x] 集数、季、字幕组、质量、语言和标题能从资源/文件名中稳定提取
- [x] 全局和订阅级命名模板、路径模板、自定义集数规则及文件名长度限制可用
- [x] 下载完成后能生成正确的媒体文件名、NFO、封面和媒体库路径
- [x] 字幕匹配、重复文件、已存在文件、洗版、完结和失败恢复行为可用
- [x] 使用临时媒体库覆盖正常、重复、缺失元数据、非法路径、跨平台路径和中途失败场景
- [x] 现有 UI 的预览、刷新和媒体结果展示无需修改

## Acceptance evidence

Media tests cover TMDB season metadata, Bangumi fallback, rename, subtitle matching, NFO/cover/still generation, supported formats, duplicate protection, completed-media movement, embedded subtitles, cover refresh and transient asset recovery. Naming/path tests cover global and subscription templates, quarter/year/season/subject/group substitutions, custom episode extraction and filename length limits. Backend HTTP tests cover scrape, batch scrape, preview, playlist, cover refresh and total-episode update against temporary metadata/media servers, including missing metadata and invalid file traversal diagnostics; cross-platform behavior is additionally checked by Linux amd64/arm64/armv7, Windows amd64 and macOS arm64 cross-compiles. Browser smoke against the Go Gateway verified login, home, settings, static assets and unchanged UI source.
