# 核心逻辑测试

核心业务边界使用两类测试持续检查：

- Go fuzz test：针对 RSS 解析与匹配、重复提交决策、torrent 元信息解析、订阅输入校验，以及已有的源站 HTML 解析。
- Mutation test：使用 Gremlins 修改 `rss`、`torrent`、`source`、`metadata`、`subscription` 五个包，确认现有断言能发现行为变化。

本地运行短时 fuzz：

```bash
FUZZ_TIME=5s bash scripts/test-core-fuzz.sh
```

运行 mutation test：

```bash
bash scripts/test-core-mutations.sh
```

Mutation test 会输出测试有效性、变异覆盖率、`LIVED` 和 `NOT COVERED` 明细，用来发现测试缺口。它不作为普通 CI 的硬失败条件，因为单个变异可能触发网络边界测试超时，结果会受机器负载影响。修改匹配、提交、源站适配、缓存刷新或订阅生命周期时，应优先查看对应包的 `LIVED` 和 `NOT COVERED` 行，并补充一个描述用户可观察行为的测试。
