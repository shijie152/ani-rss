## Agent skills

### Issue tracker

Issues and specs are tracked as local Markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

This repo uses the default triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository with a root `CONTEXT.md` and ADRs under `docs/adr/`. See `docs/agents/domain.md`.

### Scratch workspace

Use `.scratch/.tmp/` for throwaway artifacts: soak-test data, throwaway binaries, downloaded fixtures, and local logs. It is git-ignored. Do not use `/tmp` — macOS cleans it and full disks have silently wiped run state mid-verification.
