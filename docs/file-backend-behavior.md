# File Backend Behavior Contract

This document defines expected behavior for `internal/backends/file.go`.

## Output Path and Writer Lifecycle

- Enriched output path is `sourcePath + ENRICHED_FILE_SUFFIX`.
- Output directories are created automatically if missing.
- `CloseWriter(sourcePath)` closes and removes the cached writer for that source.
- After `CloseWriter`, future `Send` calls for the same source path create a new writer and continue appending.

## Entry Serialization Rules

- If `entry.Fields` is non-empty:
  - fields are marshaled as one JSON object
  - one trailing newline is appended
- If `entry.Fields` is empty and `entry.LogLine` is non-empty:
  - raw log line is written as-is
  - one trailing newline is guaranteed
- If both are empty:
  - exactly one newline is written

## Test Coverage

- `internal/backends/file_test.go`
  - `TestFileBackendSend_WritesJSONWhenFieldsExist`
  - `TestFileBackendSend_WritesRawLineWhenNoFields`
  - `TestFileBackendSend_WritesSingleNewlineForEmptyInput`
  - `TestFileBackendCloseWriter_AllowsReopenAndAppend`
