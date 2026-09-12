# 01: Go Gateway 与 Java 过渡桥接

**What to build:** 让现有 UI 通过 Go Gateway 访问 ANI-RSS，而未迁移的请求透明转发到 Java 过渡后端，使迁移可以在不改变用户入口的情况下开始。

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [x] Go Gateway 能监听当前对外端口，并可配置 Java 过渡后端地址
- [x] 现有 UI 静态资源可以从 Go Gateway 正常加载，且无需修改 UI
- [x] 未迁移的 API 请求能够转发到 Java，并保留路径、查询参数、请求体、响应体、状态码和关键响应头
- [x] Cookie、Authorization、multipart 上传、文件下载和流式响应在转发后仍然可用
- [x] Gateway 能区分 Go 路由和 Java fallback 路由，并支持按业务域切换路由归属
- [x] 建立 HTTP 黑盒测试 harness，覆盖 UI 启动、健康检查、转发成功、转发失败和连接不可用场景
- [x] 本地可以用一条可重复的命令启动 Gateway 与 Java fake backend 完成冒烟验证
