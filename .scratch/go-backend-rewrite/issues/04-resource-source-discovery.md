# 04: 资源源站发现与选择

**What to build:** 让用户通过现有 UI 搜索和选择 Mikan、AniBT、AnimeGarden 的番剧与字幕组，并可使用 Bangumi subject 创建订阅。

**Blocked by:** 02: 运行时所有权、配置与认证基础；03: 订阅管理与 JSON 数据 Store

**Status:** ready-for-agent

- [ ] Mikan 搜索、季度列表、番剧详情和字幕组/RSS 链接接口可用
- [ ] AniBT 搜索、季度列表和字幕组/RSS 链接接口可用
- [ ] AnimeGarden 列表、详情和字幕组/RSS 链接接口可用
- [ ] Bangumi 标题查询、subject 信息获取和 subject 转订阅接口可用
- [ ] 结果中的已订阅标记、标题、封面、季信息、资源信息和字幕组信息符合 UI 预期
- [ ] HTML/RSS fixture 能覆盖正常页面、空结果、字段缺失、格式变化、重定向和非成功状态
- [ ] 资源源站客户端遵守超时、重试、代理和 User-Agent 配置
- [ ] 用户可以从源站结果直接创建订阅，并在刷新页面后看到该订阅
