# 08: 通知、Emby 与事件后处理

**What to build:** 让发现资源、开始下载、下载完成、洗版和媒体整理等通知事件触发当前配置的通知与后处理动作。

**Blocked by:** 05: RSS 刷新到 qBittorrent 下载主链路；06: 刮削、媒体整理与重命名

**Status:** ready-for-agent

- [ ] Telegram、Bark、邮件、Webhook、Shell、文件移动、OpenList 上传和 Emby 刷新通知均可配置和测试
- [ ] 通知事件能携带番剧、资源、季集、字幕组、路径、元数据和处理结果
- [ ] 通知状态过滤、模板渲染、Markdown/HTML 等格式选择和延迟行为符合当前 UI 预期
- [ ] HTTP、SMTP 和 Shell 执行失败具备超时、重试、失败记录和可诊断日志
- [ ] 文件移动、OpenList 上传和本地文件删除行为遵守显式配置，失败时不会静默丢失媒体
- [ ] Emby refresh 请求和 Emby Webhook 触发的现有处理逻辑可用
- [ ] fake HTTP、SMTP 和 process boundary 测试证明同一事件不会重复发送或重复执行后处理
- [ ] 用户可以在现有通知配置页面完成新增、编辑、测试和删除
