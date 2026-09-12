# 02: 运行时所有权、配置与认证基础

**What to build:** 让 Go 接管配置、健康检查、登录、Token/API Key、IP 白名单、代理配置和日志基础能力，并建立迁移期间的任务所有权与 JSON Store 边界。

**Blocked by:** 01: Go Gateway 与 Java 过渡桥接

**Status:** ready-for-human

- [x] 当前 UI 的配置读取、配置保存、健康检查和登录流程无需修改即可工作
- [x] 登录密码、Token/session 生命周期、登录次数限制、API Key 和 IP 白名单行为符合当前外部契约
- [x] 代理开关、代理地址、代理认证和按资源源站选择代理的配置可被 Go 使用
- [x] Go 通过 Store 访问 JSON 配置，不让业务模块直接依赖 JSON 文件读写
- [x] 迁移模式能明确指定 RSS、重命名和维护任务的唯一所有者
- [x] Go 与 Java 同时运行时不会同时执行同一组定时任务，也不会同时写入同一份运行状态
- [x] 配置校验、默认值、URL 规范化、路径规范化和时区行为可通过 HTTP 测试验证
- [x] 认证失败、配置校验失败和外部服务失败都返回稳定的 UI 可消费错误结构

## Acceptance evidence

Go HTTP contract tests cover ping/config/login, redaction, invalid config, external-source failure and Bangumi episode updates; auth tests cover bearer sessions, API keys, IP rules, token revocation and the 31st failed login; proxy tests cover host matching, credentials and timeout; ownership tests cover Java/Go lock contention, stale-lock recovery, replacement-safe release and state-writer fallback. Configuration tests cover defaults, HTTP URL validation, path normalization and local-time release-date grouping. Java Maven tests and the Vue build also pass.
