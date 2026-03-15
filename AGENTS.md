# AGENTS.md

## Purpose
This file defines contributor guardrails for `Log-Enricher` so future changes do not reintroduce API drift between packages.

## Current Source of Truth
- Entry point: `main.go`
- Active log ingestion path: `internal/tailer/*` + `internal/processor/*` + `internal/pipeline/*`
- Active backend interface: `internal/backends/backend.go` (`Send(entry *models.LogEntry) error`)
- Active log entry model: `internal/models/models.go` (`models.LogEntry`)

Treat these as canonical unless a deliberate migration updates all callers and tests in one change.

## Known Legacy/Drift Areas
- Legacy watcher/multi-backend code paths were removed during the tailer migration.
- Do not reintroduce old APIs such as `backends.Manager`, `cfg.Backends`, `GetLogEntry`/`PutLogEntry`, or `internal/parser`.

Do not add new code against legacy APIs. Either:
1. Migrate legacy packages/tests to canonical interfaces, or
2. Remove legacy code in a dedicated cleanup PR.

## Required Checks Before Merging
Run all of:

```bash
go test ./...
go test -race ./...
go test -count=1 ./...
staticcheck
```

If full suite is too slow in development, at minimum run package tests for all touched packages and then `go test ./...` before merge.

## Interface-Change Policy
When changing any exported function/type used across packages:
1. Update all call sites in the same PR.
2. Update tests in the same PR.
3. Verify no stale symbols remain:

```bash
rg "OldSymbolName|old_signature_fragment"
```

No partial refactors should be merged.

## Go-Specific Rules
- Pass `context.Context` through network-bound operations.
- Avoid goroutine leaks: any spawned goroutine must have a cancellation path.
- Keep pool APIs type-safe (`*models.LogEntry`), avoid shadow types.
- Use `slog` consistently (avoid mixing with `log` unless required for compatibility).
- Prefer table-driven tests for stage behavior and file-state edge cases.

## State and Tailing Safety
- Preserve behavior in `internal/state/*` for rotation/truncation detection.
- Any change to file identity logic (`inode`, modtime, size) must include tests for:
  - unchanged file
  - inode-changed rotation
  - truncation with same inode
  - missing file

## Contributor Workflow
1. Reproduce issue with a failing test.
2. Fix implementation.
3. Add/adjust tests.
4. Run required checks.
5. Include a short compatibility note in PR description if interfaces changed.
