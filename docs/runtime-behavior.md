# Runtime Behavior Contract

This document captures expected runtime behavior across the active ingestion path:

- `main.go`
- `internal/tailer/*`
- `internal/promtailhttp/*`
- `internal/processor/*`
- `internal/pipeline/*`
- `internal/backends/backend.go`

## Application Startup (`runApplication`)

- State initialization happens before backend and pipeline setup.
- Unsupported backend values fail fast with an error.
- Invalid stage configuration fails pipeline initialization.
- Invalid app-identification regex configuration fails log manager initialization.
- When `PROMTAIL_HTTP_ENABLED=true`, the Promtail-compatible HTTP receiver is started alongside file tailing.

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
- Promtail HTTP ingestion can set a pre-parsed timestamp before pipeline processing.
- If no stage sets `Timestamp`, processor sets it to current time before sending.
- If a stage sets `Timestamp`, processor preserves that value.
- Backend send errors are returned to the caller.

## Promtail HTTP Receiver

- Routes:
  - `POST /loki/api/v1/push`
  - `POST /api/prom/push`
  - `GET /ready`
- Supported push request formats:
  - `application/x-protobuf` with raw snappy-compressed Loki `PushRequest`
  - `application/json` with Loki/Promtail stream payloads
- Supported request content encoding:
  - identity
  - gzip
- If `PROMTAIL_HTTP_BEARER_TOKEN` is configured, push routes require `Authorization: Bearer <token>`.
- Request parsing is strict and rejects malformed payloads before processing entries.
- Source path resolution from labels is sanitized and always rooted under `PROMTAIL_HTTP_SOURCE_ROOT`.

## Test Coverage

- `main_test.go`
  - `TestRunApplicationWithFileBackend`
  - `TestRunApplication_UnsupportedBackend`
  - `TestRunApplication_InvalidPipelineStage`
  - `TestRunApplication_InvalidAppIdentificationRegex`
  - `TestRunApplication_PromtailHTTPEnabled`
  - `TestRunApplication_PromtailHTTPInvalidAddress`
- `internal/promtailhttp/receiver_test.go`
  - `TestReceiver_ProtobufSnappyAndPathSanitization`
  - `TestReceiver_ProtobufSnappyGzipOnLegacyRoute`
  - `TestReceiver_JSONValuesAndGzip`
  - `TestReceiver_RejectsMalformedJSONBatchWithoutProcessing`
  - `TestReceiver_ValidationAndAuthResponses`
  - `TestReceiver_ReadyEndpoint`
- `internal/tailer/manager_test.go`
  - `TestNewManagerImpl_ValidatesAppIdentificationRegex`
  - `TestManagerImpl_GetAppNameForPath`
  - `TestManagerImpl_GetMatchingLogFiles`
  - `TestManagerImpl_StartTailingFile_IgnoresMatchingFiles`
- `internal/processor/log_processor_test.go`
  - `TestLogProcessor_ProcessLine_SetsMetadataAndFallbackTimestamp`
  - `TestLogProcessor_ProcessLine_PreservesTimestampFromPipeline`
  - `TestLogProcessor_ProcessLineWithTimestamp_UsesProvidedTimestamp`
  - `TestLogProcessor_ProcessLineWithTimestamp_PipelineCanOverrideTimestamp`
  - `TestLogProcessor_ProcessLine_PropagatesBackendErrors`
