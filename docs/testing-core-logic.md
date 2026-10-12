# 核心逻辑测试

核心业务边界使用两类测试持续检查：

- Go fuzz test：针对 RSS 解析与匹配、重复提交决策、torrent 元信息解析、订阅输入校验，以及已有的源站 HTML 解析。
- Mutation test：使用 Gremlins 修改 `rss`、`torrent`、`source`、`metadata`、`subscription`、`outbound` 六个包，确认现有断言能发现行为变化。

两者都**按改动选包**：只跑相对基线有改动的包，基线取不到、无法比对，或改动落在这些包共享的代码（`go-backend` 下其他目录、`go.mod`、核心测试脚本）上时，回落成全量——宁可多测，不可漏测。选包规则在 `scripts/changed-packages.sh`，基线优先级为 `CORE_TEST_BASE` → `origin/main` → `HEAD~1`。

强制全量：`CORE_TEST_ALL=1 bash scripts/test-core-mutations.sh`。只想看会选中哪些包：`CORE_TEST_FILES="go-backend/internal/rss/parser.go" bash scripts/changed-packages.sh rss torrent source metadata subscription outbound`。

这样做的原因：六个包合计约 1065 个变异体，每个都要重新编译并跑该包测试，全量约 8 分钟；改前端或文档时没有理由付这个成本。

变异测试固定 `--timeout-coefficient=50`：gremlins 的单个变异体超时按「本次基线耗时 × 系数」计算，默认系数在构建缓存热时会得到几十毫秒的基线，把大量变异体误判成超时（同一份代码实测：默认系数 2 killed/70 timed out，系数 50 得到 57 killed/15 lived/0 timed out）。固定系数后结果不再取决于缓存冷热。

## 何时跑变异测试

变异测试**不在 PR 主路径上**（`.github/workflows/mutation.yml`），单机串行跑满六个包约 8 分钟，加 worker 没有收益（瓶颈是变异体逐个编译、被 Go 构建缓存的锁串行化）。三种触发：

| 触发 | 范围 | 说明 |
|---|---|---|
| 每日 03:20 定时 | 全部六个包 | 分片到六个 runner，墙钟约等于最慢的 `source`（约 2.5 分钟） |
| 手动 `gh workflow run mutation` | 全部六个包 | 发布前或想复核时用 |
| 给 PR 打上 `mutation` 标签 | 改动涉及的包 | 需要 blocking 的 PR 用这一步升级门禁 |

本地想跑：`bash scripts/test-core-mutations.sh`（按改动选包）、`CORE_TEST_ALL=1 bash scripts/test-core-mutations.sh`（全量）、`bash scripts/test-core-mutations.sh rss metadata`（指定包）。

有效性下限默认 55%（`EFFICACY_THRESHOLD` 可覆盖）：实测各包 63%–84%，超时不计入有效性，所以这条线只会在「测试被改弱」时触发，不会因 CI 抖动误报。

本地运行短时 fuzz：

```bash
FUZZ_TIME=5s bash scripts/test-core-fuzz.sh
```

运行 mutation test：

```bash
bash scripts/test-core-mutations.sh
```

Mutation test 会输出测试有效性、变异覆盖率、`LIVED` 和 `NOT COVERED` 明细，用来发现测试缺口。它不作为普通 CI 的硬失败条件，因为单个变异可能触发网络边界测试超时，结果会受机器负载影响。修改匹配、提交、源站适配、缓存刷新或订阅生命周期时，应优先查看对应包的 `LIVED` 和 `NOT COVERED` 行，并补充一个描述用户可观察行为的测试。
