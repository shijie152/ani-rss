# 前端稳定性测试

前端性能改动使用三层验证：

- `pnpm test`：运行 Node 行为测试。覆盖请求编码与去重（查询参数 1000 组随机边界字符）、错误提示（含静默选项）、端点表与 UI 契约、鉴权 URL 组装、目录请求的缓存/过期刷新/失败回退、源站分组与批量添加、轮询、订阅通知、Mikan 调度和季度缓存。
- `pnpm test:mutation`：运行实际变异测试。脚本会临时修改请求编码、请求去重、请求 key（含查询串参与 key）、端点只读表、目录请求的过期刷新、鉴权 URL 的凭据位置、源站分组、复制回退、轮询并发/隐藏处理、Mikan 定时器等 14 个行为，并要求测试杀死每个变异。
- `pnpm build`：验证路由懒加载、图标按需注册、LazyImage 组件和 CSS 性能契约可以正常编译。
- `pnpm test:browser`：启动 Vite 开发服务，用 mock API 在真实 Chromium 中验证季度页动态路由与 Mikan 数据渲染，以及三个源站页（Mikan / AniBT / AnimeGarden）能从添加订阅流程打开；Mikan 页另覆盖批量添加（rssToAni + addAni）与复制磁力链。

当前结果：`pnpm test` 全绿，14/14 个 UI 变异被杀死（变异数字由 `test:mutation` 自动核对，改了变异就同步这一行）。

CI 运行浏览器测试前需要执行 `pnpm exec playwright install --with-deps chromium`。

前端端点表（`src/js/endpoints.js`）是 UI 契约的真相源：Go 侧的 `TestUIEndpointTableMatchesGoRoutes` 会逐条核对表里的路径与方法确实被后端注册（ADR-0001 的不变量守卫），改了任一侧漏改另一侧就会红。

`bash scripts/check.sh` 一条命令复现 CI 质量门：Go 侧 `go test` / `-race` / `vet` / `gofmt` / `git diff --check`，外加发布 workflow 才编译的 `-tags gui` 构建与托盘测试；前端侧在依赖已安装时跑 `pnpm test` / `test:mutation` / `build` / `test:browser`（chromium 未安装时浏览器门会显式跳过并提示）。

测试用的临时文件只写入 `.scratch/.tmp/`（`ui-mutations/` 变异体、`ui-api/` 桩加载产物），不会修改工作区源码。
