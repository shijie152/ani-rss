# 02: 深化 RSS intake module：统一获取、解析与匹配流程

**What to build:** 保持刷新、预览和 RSS 转订阅的用户可见行为不变，让 RSS feed 到已匹配资源的流程只存在于一个 deep module；主资源、备用资源、字幕组推断、offset 和匹配规则集中在同一 seam，调用侧只处理后续策略。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] 刷新、预览和 RSS 转订阅使用同一套 feed 获取、解析、字幕组推断和匹配语义
- [x] 主 RSS 与备用 RSS 的顺序、部分失败、offset、coexist、延迟下载和优先级规则保持不变
- [x] 全局排除、自定义集数规则、字幕组和资源字段补充结果保持现有契约
- [x] 预览仍不提交下载任务、不写入资源历史，并保持现有响应内容
- [x] context cancellation、重试和上游错误不会导致资源重复或错误吞并
- [x] 使用一个 seam 覆盖刷新、预览和 RSS 转订阅的组合测试
