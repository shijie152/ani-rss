# 09 — update 的 restart 走 bootstrap

**What to build:** 更新后的重启不再由处理器自己拼 `exec` 与关闭调用，而是走 bootstrap 提供的重启入口，与启动路径共享同一套顺序。

**Blocked by:** 08 — 启动与关闭收敛成 bootstrap module

**Status:** done

- [x] 处理器不再出现第二份重启实现（`os/exec` 从 management.go 移除，只剩 `restartAfterUpdate` 调注入的钩子）
- [x] 重启前资源被释放，且不产生双重关闭（bootstrap.Restart 先跑 OnRestart → 释放锁 → 再 exec；测试断言顺序）
- [x] 更新接口的对外行为不变（响应文案与 500ms 延迟保持，`check.sh` 全绿）

## Test plan

行为测试（重启入口被调用一次、关闭先于重启）+ 更新接口契约测试回归 + `bash scripts/check.sh`。
