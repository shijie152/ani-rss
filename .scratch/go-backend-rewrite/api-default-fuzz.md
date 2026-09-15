# Java / Go 默认行为与 HTTP Fuzz 验收

更新时间：2026-09-15

## 范围

本轮针对现有 Java Controller 的 71 个公开路径，增加了安全的默认行为探针。会删除数据、写配置、写订阅、刷新下载或重启进程的成功路径不进入随机 fuzz；这些路径继续由 fake server、临时目录和生命周期测试覆盖。

实现位置：

- `go-backend/internal/backend/api_parity_test.go`
- `go-backend/internal/backend/api_default_parity_test.go`

## 默认行为矩阵

`FuzzJavaGoAPIDefaults` 对 48 个安全 API 场景组合测试以下边界：

- 无 body、`{}`、`null`、`[]`、截断 JSON 和非 JSON
- 缺失 query 参数、无关 query 参数和空默认输入
- JSON Result 的 transport status、业务 `code`、`data` 是否存在及类型
- 文件响应的 Content-Type 和 Content-Disposition
- 空配置、空订阅、未配置下载器、无 multipart 请求

比较时忽略时间戳、随机 token、版本号和运行实例产生的日志数量；不会把 Java 泄漏的异常文本作为 Go 的强制实现目标。

## 运行结果

在全新临时配置目录启动 Java 3.2.32 和最新 Go 二进制后：

```sh
ANI_RSS_JAVA_URL=http://127.0.0.1:<JAVA_PORT> \
ANI_RSS_GO_URL=http://127.0.0.1:<GO_PORT> \
go test ./internal/backend -run '^TestJavaGoAPIParity$' -count=1
```

- 非变更确定性差分全部通过；变更探针默认跳过。
- `FuzzJavaGoAPIDefaults -fuzztime=10s -parallel=1`：基线覆盖显示 `71/71`，完成 748 次随机执行并通过。
- 前一轮 30 秒运行完成 2,309 次执行后发现的失败属于旧测试实例/探针状态污染，不作为实现差异；使用最新二进制和全新实例复测已通过。

本轮实际修复：

1. `exportConfig` 差分比较归一化备份文件名中的版本号，消除 `3.2.32` 与 `dev` 的误报。
2. Go 统一 JSON 解码拒绝 `null` body，与 Spring 必填 `@RequestBody` 一致。
3. 默认差分测试把会改变状态的接口改为 opt-in：设置 `ANI_RSS_PARITY_INCLUDE_MUTATIONS=1` 才执行，且必须使用一次性服务实例。
4. 修正默认 fuzz 探针的认证标记，避免错误请求累计触发 Go 登录保护。

## 回归结果

- `go test ./...`：通过
- `go test -race ./...`：通过
- `go vet ./...`：通过
- `npm test`：7/7 通过
- `npm run build`：通过
- `./scripts/smoke-go-release.sh`：通过，交叉编译、测试和启动 smoke 全部通过
- 7789 最新 Go 服务：`GET /api/ping` 返回 `code=200`，现有 UI 静态首页可访问

## 结论

在当前最新功能范围内，默认/边界请求已完成 Java/Go 的可重复 HTTP 验收；没有发现尚未定位的默认行为差异。外部源站、真实下载器、通知服务和写操作仍以隔离 fake 服务或明确生命周期测试作为验收依据，不使用线上依赖阻塞确定性测试。
