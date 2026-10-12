# 前端稳定性测试

前端性能改动使用以下质量门：

- `pnpm test`：运行 Node 行为测试。覆盖请求编码与去重（查询参数 1000 组随机边界字符）、错误提示（含静默选项）、端点表与 UI 契约、鉴权 URL 组装、目录请求的缓存/过期刷新/失败回退、源站分组与批量添加、轮询、订阅通知、Mikan 调度和季度缓存。
- `pnpm test:mutation`：运行实际变异测试。脚本会临时修改请求编码、请求去重、请求 key（含查询串参与 key）、端点只读表、目录请求的过期刷新、鉴权 URL 的凭据位置、源站分组、复制回退、轮询并发/隐藏处理、Mikan 定时器等 14 个行为，并要求测试杀死每个变异。
- `pnpm build`：验证路由懒加载、图标按需注册、LazyImage 组件和 CSS 性能契约可以正常编译。
- `pnpm test:browser`：启动 Vite 开发服务，用 mock API 在真实 Chromium 中验证季度页动态路由和 Mikan 数据渲染，以及三个源站页（Mikan / AniBT / AnimeGarden）能从添加订阅流程打开；Mikan 页另覆盖批量添加（rssToAni + addAni）与复制磁力链。
- `pnpm test:visual`：使用固定 mock 数据和固定视口执行 Chromium 视觉回归，覆盖桌面浅色、移动端深色和资源弹窗。测试按当前操作系统查找三张对应基线；缺少基线会失败并提示使用 `--update-snapshots` 生成，确保视觉回归不会静默跳过。截图断言允许最多 400 个差异像素：实测季节选择器与旁边的数量在字体平滑状态不同时会重绘约 300 个字形像素，而真正的布局变化会移动上万个像素。

当前结果：86 个 Node 测试通过，14/14 个 UI 变异被杀死；视觉回归包含 3 个截图场景。

CI 运行浏览器测试前需要执行：

```bash
pnpm exec playwright install --with-deps chromium
```

前端端点表（`src/js/endpoints.js`）是 UI 契约的真相源：Go 侧的 `TestUIEndpointTableMatchesGoRoutes` 会逐条核对表里的路径与方法确实被后端注册（ADR-0001 的不变量守卫），改了任一侧漏改另一侧就会红。

`bash scripts/check.sh` 一条命令复现 CI 质量门：Go 侧 `go test` / `-race` / `vet` / `gofmt` / `git diff --check`，外加发布 workflow 才编译的 `-tags gui` 构建与托盘测试；前端侧在依赖已安装时跑 `pnpm test` / `test:mutation` / `build` / `test:browser`（chromium 未安装时浏览器门会显式跳过并提示）。

测试用的临时文件只写入 `.scratch/.tmp/`（`ui-mutations/` 变异体、`ui-api/` 桩加载产物），不会修改工作区源码。

首次生成或有意更新视觉基线：

```bash
pnpm --dir ani-rss-ui exec playwright test tests/browser/visual-regression.spec.mjs --update-snapshots
```

基线文件按 Playwright 的平台后缀分别保存（例如 `-darwin.png`、`-linux.png`）。每个 CI/开发目标操作系统都应生成并提交自己的三张基线；缺少平台基线时 `test:visual` 会失败，而不是跳过断言。

视觉差异失败时，使用以下命令查看实际图、基线图和差异图：

```bash
pnpm --dir ani-rss-ui exec playwright show-report
```
