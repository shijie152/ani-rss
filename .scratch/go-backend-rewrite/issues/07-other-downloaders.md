# 07: Transmission、Aria2 与 OpenList 下载器适配

**What to build:** 让用户无需修改 UI 即可切换并使用 Transmission、Aria2 或 OpenList，完成与 qBittorrent 等价的下载任务生命周期管理。

**Blocked by:** 05: RSS 刷新到 qBittorrent 下载主链路

**Status:** ready-for-agent

- [ ] 三种下载器都实现统一的登录测试、资源提交、任务列表、状态查询和错误映射
- [ ] Transmission 的 session id、RPC 请求、删除、重命名、标签和保存路径行为可用
- [ ] Aria2 的 JSON-RPC、磁力/种子提交、状态、删除、保存路径和 Tracker 更新行为可用
- [ ] OpenList 的离线下载、任务轮询、重试、远端文件检查、重命名和移动行为可用
- [ ] 下载器切换只改变配置，不要求修改 UI 或 RSS 匹配逻辑
- [ ] 每种下载器都有独立 fake server，覆盖认证失败、超时、重试、异常状态和部分成功
- [ ] 下载器返回的状态都能映射到统一的下载任务生命周期
- [ ] 真实下载器冒烟测试验证主链路不会因 fake server 与实际协议差异而失效
