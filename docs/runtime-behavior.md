# Runtime Behavior Contract

This document captures expected runtime behavior across the active ingestion path:

- `main.go`
- `internal/tailer/*`
- `internal/processor/*`
- `internal/pipeline/*`
- `internal/backends/backend.go`

## Application Startup (`runApplication`)

- State initialization happens before backend and pipeline setup.
- Unsupported backend values fail fast with an error.
- Invalid stage configuration fails pipeline initialization.
- Invalid app-identification regex configuration fails log manager initialization.

## Tailer Manager

- App name resolution order:
  - `APP_NAME` (static value) if configured
  - `APP_IDENTIFICATION_REGEX` named capture group `app` if configured
  - parent directory name of the log file path
  - fallback `"log-enricher"` when parent directory is unusable
- File discovery is recursive and only includes configured `LOG_FILE_EXTENSIONS`.
- Files matching `LOG_FILES_IGNORED` are not tailed.

## Processor

- Every processed entry carries:
  - `SourcePath` from the tailed file path
  - `App` from manager-selected app name
- If no stage sets `Timestamp`, processor sets it to current time before sending.
- If a stage sets `Timestamp`, processor preserves that value.
- Backend send errors are returned to the caller.

## Test Coverage

- `main_test.go`
  - `TestRunApplicationWithFileBackend`
  - `TestRunApplication_UnsupportedBackend`
  - `TestRunApplication_InvalidPipelineStage`
  - `TestRunApplication_InvalidAppIdentificationRegex`
- `internal/tailer/manager_test.go`
  - `TestNewManagerImpl_ValidatesAppIdentificationRegex`
  - `TestManagerImpl_GetAppNameForPath`
  - `TestManagerImpl_GetMatchingLogFiles`
  - `TestManagerImpl_StartTailingFile_IgnoresMatchingFiles`
- `internal/processor/log_processor_test.go`
  - `TestLogProcessor_ProcessLine_SetsMetadataAndFallbackTimestamp`
  - `TestLogProcessor_ProcessLine_PreservesTimestampFromPipeline`
  - `TestLogProcessor_ProcessLine_PropagatesBackendErrors`
