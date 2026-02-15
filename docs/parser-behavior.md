# Parser Behavior Contract

This document describes the expected runtime behavior of the active parser stages in `internal/pipeline`.

## Shared Parser Guarantees

The `json_parser` and `structured_parser` stages share these guarantees:

- They always return `keep=true` and do not drop log lines.
- If `entry.Fields` is already non-empty, parsing is skipped and existing fields are preserved.
- They use a persisted success/failure cache keyed by:
  - `sourcePath + firstByte(logLine)` when the log line is non-empty
  - `sourcePath + ":empty"` when the log line is empty
- If a cache entry exists with `success=false`, parsing is skipped for matching future lines.

## `json_parser` Behavior

- Parsing is attempted only when the first byte is `{`.
- On valid JSON object input, decoded keys are written to `entry.Fields`.
- On invalid JSON input (when parsing is attempted), `entry.Fields` is reset to an empty map.
- Non-JSON-prefixed lines are passed through unchanged.

Examples:

- `{"level":"info"}` -> `Fields["level"] == "info"`
- `{"broken":` -> `Fields` becomes empty
- `level=info` -> no JSON parse attempt

## `structured_parser` Behavior

### Construction

- If no `pattern` is configured, default pattern is used:
  - `(\w+)=(".*?"|\S+)`
- Configured regex must be one of:
  - a regex with named capture groups, or
  - exactly two unnamed capture groups (key + value mode)
- Invalid regex syntax or invalid capture shape returns an error at stage creation.

### Processing

- Named group mode:
  - A single regex match is attempted.
  - Named groups are copied into `entry.Fields`.
- Key/value mode (two unnamed groups):
  - Iteratively parses all key/value matches.
  - Quoted values are unquoted.
  - Any non-whitespace unmatched text causes parse failure.
  - On failure, partially parsed fields are cleared.

Examples:

- `level=info msg="hello world"` -> `{ "level": "info", "msg": "hello world" }`
- `level=info trailing-text` -> parse fails and fields are cleared
- Named pattern: `level=(?P<level>\w+)\s+msg=(?P<msg>.*)`

## Test Coverage

Contract tests are implemented in `internal/pipeline/parser_edge_test.go`.
