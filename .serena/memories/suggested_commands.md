# Suggested commands
## Core development
- `go run .` — run the main application from the repo root.
- `go test ./...` — standard full test suite.
- `go test -race ./...` — required race-detector suite before merge.
- `go test -count=1 ./...` — required uncached verification before merge.
- `staticcheck ./...` — required lint/static analysis before merge. In CI/devcontainer setup, `staticcheck` may first be installed with `go install honnef.co/go/tools/cmd/staticcheck@latest`.

## Useful repo checks
- `git status` — inspect working tree.
- `git diff` — inspect local changes.
- `rg "pattern"` — fast symbol/string search.
- `ls`, `pwd` — basic Linux navigation/inspection.

## Container/docs-oriented usage from README
- `docker build -t log-enricher .` — build image locally.
- `docker run --rm -v "$(pwd)/logs:/logs" -v "$(pwd)/cache:/cache" -e BACKEND=file ghcr.io/l3tum/log-enricher` — example container execution pattern (see README for fuller env setups).

## Notes
- Promtail receiver endpoints documented in README: `POST /loki/api/v1/push` and `POST /api/prom/push`.
- For repo exploration, prefer `rg` over slower recursive shell searches on Linux.