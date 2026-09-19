# Coding standards

Read during review, not implementation. These are the rules a reviewer enforces on a diff. Anything already stated in `docs/adr/` wins; this file points at it rather than restating it.

## UI / HTTP contract is frozen

The existing Vue UI must work unchanged. Do not change public API paths, HTTP methods, request/response JSON shapes, the `Result` envelope, auth, or the documented external endpoints (Emby, ICS, API key, MCP, Swagger). See `docs/adr/0001-go-backend-gradual-replacement.md`. A diff that alters any of these is a defect.

- Config keys keep the names the UI posts (e.g. `downloadToolType`, `downloadPathTemplate`).
- New behavior goes in scheduler-driven side effects, not by changing an existing route's contract.

## Parity with the Java baseline

Where the Go service reimplements Java behavior, the Java semantics are the source of truth. When a reviewer can't tell whether a difference is a bug or an improvement, it's a bug. Deliberate deviations must be called out in the commit/PR and in code comments.

- Downloader adapters: match Java's `BaseDownload`/per-tool success and error semantics. A successful response is whatever Java treats as success (e.g. HTTP 2xx), not a stricter body match.
- Torrent lifecycle states use Java's names (`stalledUP`, `stoppedUP`, `forcedUP`, `queuedUP`, `uploading`) and the `下载完成`/`RENAME`/`备用RSS`/`ani-rss` tags.
- The store is SQLite at `ani-rss.sqlite` (ADR-0002); nothing else writes app state.

## Behavior lives behind module seams, not in handlers

- `internal/backend/app.go` is a thin route shell. Business logic belongs in the owning package (`rss`, `completion`, `media`, `downloader`, `subscription`, `notification`, `scheduler`, `source`, `store`).
- Long-running work runs in scheduler jobs or background tasks, never inline in an HTTP handler.
- One process owns each scheduler domain via the ownership locks; don't bypass `ownership.Manager` for periodic work.

## Quality gate

Every change must pass `./scripts/check.sh` (test, race, vet, gofmt, diff check) before review is called done.
