# Java / Go API 对照矩阵

更新时间：2026-09-15

本文以 Java Controller 映射和 Go `App.Routes()` 为准，记录最终切换前需要验证的 71 个公开 API 路径。`/api/ping` 的多个 HTTP 方法合并为一行；MCP 和 Swagger 是 Go 的可选扩展，不计入 Java Controller 路径数量。

## 判定规则

- 两端的传输状态、业务 `code`、稳定 `message`、数据类型和关键字段必须一致；时间戳、JWT、随机 API Key、版本号和日志文本不参与比较。对于 Java 仅泄漏 NPE/越界异常文本的非法输入，差分测试比较传输状态、业务 `code` 和 `data` 形状，Go 保留稳定的校验错误。
- Java 的 Result 错误通常是 HTTP 200、JSON `code=500/403/404`；Go 必须保留这个外部行为。
- “无副作用”探针只发送空请求、缺参请求或固定的无效输入；带外部调用、写配置、写订阅、写媒体或控制进程的 API 需要 fake server/临时目录验收。
- 可重复差分命令：

  ```sh
  ANI_RSS_JAVA_URL=http://127.0.0.1:<JAVA_PORT> \
  ANI_RSS_GO_URL=http://127.0.0.1:<GO_PORT> \
  go test ./go-backend/internal/backend -run '^TestJavaGoAPIParity$' -count=1
  ```

  该测试默认跳过，不会在普通单元测试中访问外部服务。两端必须使用同一份干净配置和订阅数据；若 Java 数据含有损坏的 `[{}]` 订阅，`listAni`、ICS 和日志结果不能作为实现差异证据。

## API 清单

| # | 路径 | Java | Go | 默认/边界探针 | 副作用与结论 |
|---:|---|---|---|---|---|
| 1 | `/api/ping` | GET/POST/PUT/DELETE/PATCH/OPTIONS | runtime；同方法 | 普通请求、OPTIONS 空响应 | 无副作用；Result 与 OPTIONS 空响应已对齐 |
| 2 | `/api/login` | POST | runtime | 正确密码、错误密码、空 JSON | 无副作用；认证与失败 code 已覆盖 |
| 3 | `/api/config` | POST | runtime | 已认证读取、未认证读取 | 只读；比较脱敏后的稳定配置字段 |
| 4 | `/api/setConfig` | POST | runtime | 空对象、非法 URL、脱敏密码回写 | 写配置；需验证 SQLite 事务和 token 保留 |
| 5 | `/api/testIpWhitelist` | POST | runtime | 本机 IP、白名单关闭/开启 | 只读；比较 code/message |
| 6 | `/api/custom.js` | GET | runtime | 空自定义脚本 | 只读；默认正文 `// empty js` 与类型已对齐 |
| 7 | `/api/custom.css` | GET | runtime | 空自定义样式 | 只读；默认正文 `/* empty css */` 与 `text/css` 已对齐 |
| 8 | `/api/testProxy` | POST | runtime | 缺 `url`、非法 Base64、合法 fake URL | 外部 HTTP；缺参使用 Java String 参数错误 |
| 9 | `/api/logs` | POST | runtime | 空请求、日志上限 | 只读；需同一运行数据比较数量和字段 |
| 10 | `/api/clearLogs` | POST | runtime | 空请求 | 写运行状态；清理后应返回无 `data` 的成功 Result |
| 11 | `/api/downloadLogs` | GET | runtime | 空日志、带日志 | 文件响应；比较类型、文件名和可读性 |
| 12 | `/api/clearCache` | POST | runtime | 孤立文件、被引用文件 | 删除缓存文件；已有临时目录黑盒测试 |
| 13 | `/api/trackersUpdate` | POST | runtime | 空 trackers、fake qBittorrent | 外部 HTTP/写下载器；已有 fake 协议测试 |
| 14 | `/api/exportConfig` | GET | runtime | 干净配置、带缓存/任务 | ZIP 文件；比较 Content-Disposition 和条目集合 |
| 15 | `/api/importConfig` | POST | runtime | 缺文件、非法 ZIP、合法 ZIP、越界条目 | 替换配置/订阅；已有大小和路径限制测试 |
| 16 | `/api/proxyImage` | GET | runtime | 缺 `imgUrl`、非法 URL、fake 图片 | 外部 HTTP/写图片缓存；SSRF 和类型已覆盖 |
| 17 | `/api/calendar.ics` | GET | runtime | 空订阅、有效订阅、API Key | 只读文件响应；需干净订阅数据比较 |
| 18 | `/api/about` | POST | runtime | 空请求 | 只读；版本字段按构建版本比较 |
| 19 | `/api/update` | POST | runtime | 无更新地址、fake 更新源 | 下载/重启；仅 fake server 验证，不直接调用线上源 |
| 20 | `/api/stop` | POST | runtime | 缺 `status`、`status=0/1`、非法值 | 控制进程；缺参 Integer 错误已对齐，非法值两端均为 500，Go 不泄漏 Java 越界异常，真实重启不在自动探针执行 |
| 21 | `/api/webui/upload` | POST | runtime | 缺文件、非 ZIP、合法 ZIP | 写 WebUI；已有 ZIP 安全测试 |
| 22 | `/api/webui/delete` | POST | runtime | 空请求、无自定义 WebUI | 删除 WebUI；已有生命周期测试 |
| 23 | `/api/webui/getUpdate` | POST | runtime | 无更新、fake 更新源 | 外部 HTTP；比较稳定更新字段 |
| 24 | `/api/webui/update` | POST | runtime | 无更新、fake 更新包 | 写 WebUI/重启；使用 fake 更新源验收 |
| 25 | `/api/testNotification` | POST | runtime | 空配置、fake Webhook/SMTP | 外部通知；已有重试和失败测试 |
| 26 | `/api/newNotification` | POST | runtime | 默认对象字段集合 | 无副作用；`embyHost` 空字段已与 Java null 省略行为对齐 |
| 27 | `/api/getTgUpdates` | POST | runtime | 空 token、fake Telegram updates | 外部 HTTP；空 token 和聊天去重已覆盖 |
| 28 | `/api/getEmbyViews` | POST | runtime | 缺 host/key、fake Emby | 外部 HTTP；参数校验和返回列表已覆盖 |
| 29 | `/api/embyWebHook` | POST | runtime | 空事件、禁用 BGM、匹配事件 | 写 BGM/异步事件；已有成功 no-op 与更新测试 |
| 30 | `/api/listAni` | POST | subscriptions | 空订阅、有效订阅、损坏旧记录 | 只读；返回七个 weekday bucket 和 total |
| 31 | `/api/addAni` | POST | subscriptions | 空对象、最小有效订阅、启用/禁用 | 写订阅/可能刷新；已有持久化与副作用测试 |
| 32 | `/api/setAni` | POST | subscriptions | 不存在 ID、有效修改、路径移动 | 写订阅/可移动媒体；需临时媒体库验收 |
| 33 | `/api/deleteAni` | POST | subscriptions | 缺 `deleteFiles`、空 ID 列表、删除文件 true/false | 写订阅/可选清理；Boolean 缺参已对齐 |
| 34 | `/api/batchEnable` | POST | subscriptions | 缺 `value`、空列表、有效列表 | 写订阅；Boolean 缺参已对齐 |
| 35 | `/api/updateTotalEpisodeNumber` | POST | subscriptions | 缺 `force`、空列表、fake BGM | 写订阅/外部 HTTP；先校验 Boolean，再拒绝空列表，避免空选择启动后台任务 |
| 36 | `/api/importAni` | POST | subscriptions | 非法 JSON、重复 ID、冲突策略 | 写订阅；冲突和校验已有覆盖 |
| 37 | `/api/downloadPath` | POST | subscriptions | 空对象、全局/自定义路径模板 | 只读计算；空对象现在返回稳定校验错误，正常请求比较路径字段和平台分隔符规则 |
| 38 | `/api/mikan` | POST | sources | 缺 `text`、空搜索、季度筛选、fake HTML | 外部 HTTP；Mikan 季度列表和空结果已修复/覆盖 |
| 39 | `/api/mikanGroup` | POST | sources | 缺 `url`、fake 详情页 | 外部 HTTP；String 缺参已对齐 |
| 40 | `/api/aniBT` | POST | sources | 空查询、标题查询、季度查询、fake JSON | 外部 HTTP；过滤、排序和周顺序已有测试 |
| 41 | `/api/aniBTGroup` | POST | sources | 缺 `bgmId`、fake groups | 外部 HTTP；String 缺参已对齐 |
| 42 | `/api/animeGardenList` | POST | sources | 无 `bgmUrl`、有 `bgmUrl`、fake subjects | 外部 HTTP；Bangumi 封面覆盖为 best-effort |
| 43 | `/api/animeGardenGroup` | POST | sources | 缺 `bgmId`、fake resources | 外部 HTTP；分页、分组和 RSS URL 已覆盖 |
| 44 | `/api/searchBgm` | POST | sources | 缺 `name`、空名称、fake Bangumi | 外部 HTTP；String 缺参已对齐 |
| 45 | `/api/getAniBySubjectId` | POST | sources | 缺 `id`、fake subject | 外部 HTTP/生成订阅对象；String 缺参已对齐 |
| 46 | `/api/getBgmTitle` | POST | sources | 空对象、fake subject/TMDB | 外部 HTTP；比较标题和缺数据错误 |
| 47 | `/api/rate` | POST | sources | 空对象、fake BGM rating | 外部 HTTP；比较 null/number data |
| 48 | `/api/setRate` | POST | sources | 空对象、有效 score、fake BGM | 外部 HTTP/写远端评分；不使用线上账号做自动测试 |
| 49 | `/api/meBgm` | POST | sources | 未授权/无 token、fake BGM | 外部 HTTP；账号状态需 fake 服务验证 |
| 50 | `/api/bgm/oauth/callback` | POST | sources | 缺 `code`、fake OAuth token 响应 | 写配置/外部 HTTP；String 缺参已对齐 |
| 51 | `/api/rssToAni` | POST | sources | 空对象、Mikan/AniBT/Garden/other URL、fake RSS | 外部 HTTP/生成订阅对象；空地址错误已对齐 |
| 52 | `/api/refreshAll` | POST | rss | 空订阅、fake RSS/QB | 后台刷新/下载；只能由一个 owner 执行 |
| 53 | `/api/refreshAni` | POST | rss | 不存在 ID、fake RSS/QB | 后台刷新/下载；已有单订阅测试 |
| 54 | `/api/previewAni` | POST | rss | 空订阅、fake RSS | 只读预览/外部 HTTP；空 RSS 地址现在先返回稳定校验错误，正常请求比较 preview 数据类型 |
| 55 | `/api/deleteTorrent` | POST | rss | 缺 `id`/`hash`、不存在订阅、有效 hash | 删除缓存和资源记录；两个 String 缺参已对齐 |
| 56 | `/api/torrentsInfos` | POST | rss | 未配置 QB、fake QB、认证失败 | 只读下载器/保存快照；未配置时返回空列表 |
| 57 | `/api/downloadLoginTest` | POST | rss | 空配置、fake QB/其他下载器 | 外部 HTTP；比较登录成功/失败 code |
| 58 | `/api/startCollection` | POST | media | 空合集、fake metainfo/QB | 下载/写任务；已有合集端到端测试 |
| 59 | `/api/previewCollection` | POST | media | 空合集、非法 bencode、fake metainfo | 只读解析；已有路径安全和匹配测试 |
| 60 | `/api/getCollectionSubgroup` | POST | media | 空合集、fake metainfo | 只读解析；比较字符串/空值行为 |
| 61 | `/api/scrape` | POST | media | 缺 `force`、空 Ani、fake TMDB/BGM | 后台写订阅/媒体；Boolean 缺参已对齐 |
| 62 | `/api/batchScrape` | POST | media | 缺 `force`、空列表、fake TMDB/BGM | 后台写订阅/媒体；校验顺序和 Boolean 缺参已对齐 |
| 63 | `/api/refreshCover` | POST | media | 空 Ani、fake 图片、下载失败 | 写封面缓存；失败回退 cover.png 已覆盖 |
| 64 | `/api/getThemoviedbName` | POST | media | 空对象、ID/标题查询、fake TMDB | 外部 HTTP；比较标题、ID 和错误 code |
| 65 | `/api/getThemoviedbGroup` | POST | media | 空对象、空 TMDB ID、fake TMDB | 外部 HTTP；`tmdb is null` 空输入已对齐 |
| 66 | `/api/playList` | POST | media | 不存在订阅、临时媒体库、字幕文件 | 只读媒体扫描；视频/字幕/图片识别已有测试 |
| 67 | `/api/getSubtitles` | POST | media | 缺 filename、非法 Base64、不存在 MKV、内封字幕 | 只读媒体解析；缺参和空成功列表已对齐 |
| 68 | `/api/file` | GET | media | 缺 filename、非法路径、symlink、Range、图片 | 读文件；缺参、授权、MIME、缓存和 Range 已覆盖 |
| 69 | `/api/upload` | POST | media | 缺文件、文本/媒体文件、超大文件 | 临时文件/媒体上传；比较扩展名和 Result |
| 70 | `/api/uploadAndRead` | POST | media | 缺文件、UTF-8 文本、超大文件 | 只读上传内容；比较字符串 data |
| 71 | `/api/uploadAndReadToBase64` | POST | media | 缺文件、二进制文件、超大文件 | 只读上传内容；比较 Base64 data |

## 当前结论

- 路由面：Go 已覆盖 Java 的 71 个路径；`/api/ping` 额外覆盖 Java 实际接受的 PUT、DELETE、PATCH 和 OPTIONS。
- 差分面：`TestJavaGoAPIParity` 覆盖确定性读取、文件头、空请求、缺参、非法值和无 multipart 请求；会改写状态的探针默认跳过，只有设置 `ANI_RSS_PARITY_INCLUDE_MUTATIONS=1` 并使用一次性实例时才执行。外部调用、上传、ZIP、媒体和控制进程仍由 fake server/临时目录测试覆盖。
- 默认行为 fuzz：`FuzzJavaGoAPIDefaults` 覆盖 47 个安全 API 场景、空/`{}`/`null`/`[]`/非法 JSON 以及无关 query 参数；在一次 10 秒运行中完成 311 次随机执行并通过。测试会比较 transport status、业务 code、JSON/文件响应形状和 Content-Type，忽略时间戳与实例数据量。
- 已确认并修复的边界差异：缺少必需 query 参数、TMDB 空输入、RSS 空地址、`newNotification` 的 null 字段、JSON/静态资源 Content-Type。
- 本轮新增修复：`downloadPath` 拒绝空标题、`previewAni` 拒绝空 RSS 地址、`updateTotalEpisodeNumber` 拒绝空选择且保持缺参校验顺序；三项均有 HTTP 回归测试。
- 季度页空白问题的前端修复位于 `SeasonCatalogView.vue` 和 `seasonCatalogCache.js`：筛选响应不再覆盖季度选项，包含有效番剧但没有季度数组时仍可缓存和展示。
- 早期被污染的 Java 实例不再作为证据；已使用相同的干净状态重跑差分测试，结果见下方验收记录。

## 2026-09-15 验收记录

- 在全新临时数据目录启动 Java/Go（分别为本地临时端口），`TestJavaGoAPIParity` 的非变更确定性探针全部通过；变更探针默认跳过以避免互相污染，写操作由独立生命周期测试覆盖。Java 泄漏 NPE/越界文本的非法输入按结果形状验收。
- 默认行为 fuzz 在全新临时 Java/Go 实例上完成 311 次随机执行并通过；过程中发现并修复了 Go 对 JSON `null` 和空 Emby webhook body 的边界处理差异。
- 真实源站复测：Mikan 当前季度返回 53 个季度选项、8 个分组、95 部番剧；AniBT 返回 7 个季度选项、7 个星期分组。Java 与 Go 的星期分组数量及内容一致。
- Mikan 季度筛选请求（2026 夏、2026 春）与 Java 的每个分组数量一致；筛选响应不重复返回季度选项，前端会保留首次加载的选项。
- Playwright 浏览器 smoke：7789 显示 95 部、8 个分组；首屏前 3 张封面均为已加载的 400×400 图片；伪造的“主页/订阅/列表”卡片数量为 0。通过“查看资源 → 添加”后，新增订阅表单的标题、BgmUrl、RSS、日期、季、偏移和总集数均有值，控制台无错误。
- Go 全量单测、`-race`、`go vet`、8 个 fuzz seed replay、六目标交叉编译和 Go-only 启动/UI smoke 均通过。
- 代理边界：7789 当前配置启用了 `192.168.1.120:7890`，该代理到 Mikan 的 TLS 连接会间歇性返回 EOF；同一代理访问 AniBT、Bangumi、TMDB 正常，关闭代理的 Go 实例和浏览器成功请求 Mikan。此项属于代理/上游连接稳定性，不能通过绕过用户代理配置来修复；已有重试及前端成功缓存回退。
