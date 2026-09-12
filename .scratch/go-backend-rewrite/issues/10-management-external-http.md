# 10: 管理、备份、上传与外部 HTTP 接口

**What to build:** 让 Go 接管剩余管理能力和外部 HTTP 入口，包括日志、备份恢复、Web UI 管理、图片代理、Tracker、自动更新、ICS 和 Emby Webhook。

**Blocked by:** 02: 运行时所有权、配置与认证基础；03: 订阅管理与 JSON 数据 Store；06: 刮削、媒体整理与重命名

**Status:** ready-for-agent

- [ ] 日志查看、清理、下载、级别和保留策略接口可用
- [ ] 配置导出/导入、ZIP 备份和恢复接口可用，并遵守上传大小限制
- [ ] 自定义 Web UI 的上传、更新、删除和 custom.js/custom.css 行为可用
- [ ] 图片代理能遵守代理列表、认证、超时和内容类型规则
- [ ] Tracker 手动更新和自动更新行为可用，且能向当前下载器分发 Tracker
- [ ] ICS 日历接口能返回当前订阅日程，并支持 API Key 访问
- [ ] Emby Webhook 接口能完成当前外部事件处理，并支持 API Key 或现有认证方式
- [ ] 自动更新、停止/重启和关于信息接口返回当前 UI 可消费的结果
- [ ] 日志、备份、上传、代理、ICS 和 Webhook 均有成功、失败、鉴权和边界输入黑盒测试
