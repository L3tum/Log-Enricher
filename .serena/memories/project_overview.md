# Log-Enricher overview
- Purpose: Go service that tails log files, processes lines through a configurable enrichment/filter pipeline, and writes enriched results to either local files or Grafana Loki.
- It can also receive Promtail/Loki push traffic over HTTP and send those entries through the same pipeline.
- Canonical source-of-truth areas from `AGENTS.md`: entrypoint `main.go`; ingestion path `internal/tailer/*`, `internal/processor/*`, `internal/pipeline/*`; backend interface `internal/backends/backend.go` with `Send(entry *models.LogEntry) error`; log model `internal/models/models.go`.
- Runtime flow in `main.go`: load env config, create lifecycle context, install signal handling, initialize persisted state, choose backend (`file` or `loki`), configure slog-backed logging, build pipeline manager, optionally start Promtail HTTP receiver, start tailer manager, and on shutdown stop HTTP receiver, save state, and shut down backend.
- Supported behavior called out in docs: recursive log discovery, resume/state tracking across restarts, JSON and structured parsing, timestamp extraction, client IP extraction, hostname enrichment, GeoIP enrichment, template-based enrichment, and filter stages.
- Primary deployment style in README is containerized, but standard Go local execution is also available via the root entrypoint.
- Platform context for development: Linux.