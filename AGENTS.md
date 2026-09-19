## Agent skills

### Issue tracker

Issues and specs are tracked as local Markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

This repo uses the default triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository with a root `CONTEXT.md` and ADRs under `docs/adr/`. See `docs/agents/domain.md`.

### Scratch workspace

Use `.scratch/.tmp/` for throwaway artifacts: soak-test data, throwaway binaries, downloaded fixtures, and local logs. It is git-ignored. Do not use `/tmp` — macOS cleans it and full disks have silently wiped run state mid-verification.

### 长任务与轮询

跑后台任务或轮询时，用 `nohup … > file 2>&1 &` 启动再用 `tail` 读结果，不要在 REPL 里内联 `sleep`（超过约 25 秒会触发内核超时并重置会话）。
