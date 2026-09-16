# 04: 统一 Bangumi / TMDB metadata module

**What to build:** 保持订阅创建、刮削和标题展示行为不变，让 Bangumi 与 TMDB 的选择、归一化、缓存和失败回退由唯一的 metadata module 管理；资源源站和订阅刮削只消费统一的元数据结果，不再各自维护 Bangumi 访问逻辑。

**Blocked by:** 01: 深化资源源站 module：稳定 facade、adapter 与缓存策略

**Status:** ready-for-human

- [x] Bangumi subject 的标题、原名、封面、评分、日期、季数和总集数保持现有结果
- [x] TMDB 搜索、详情、剧集元数据和 Bangumi fallback 的优先级保持不变
- [x] 资源源站转换、订阅创建、刮削和标题格式化使用同一 metadata authority
- [x] 元数据服务的缓存、失败回退和 stale 行为不会把本地订阅状态写入长期缓存
- [x] UI 需要的完整原始字段继续可用，既有 HTTP interface 不变
- [x] 覆盖 TMDB 可用、TMDB 不可用、Bangumi fallback、缺少 subject 和封面缺失等路径
