package processor

import (
	"encoding/json"
	"errors"
	"log-enricher/internal/backends"
	"log-enricher/internal/models"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/state"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain sets up the slog logger for tests.
func TestMain(m *testing.M) {
	// Create a new logger that writes to stdout with debug level.
	// This will make slog.Debug messages visible during test execution.
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	// Run all tests
	exitCode := m.Run()

	// Exit with the test results
	os.Exit(exitCode)
}

// testPipeline is a minimal, non-mock implementation of the ProcessPipeline interface
// for integration testing of the LogProcessor with real pipeline stages.
type testPipeline struct {
	stages []pipeline.Stage
}

type captureBackend struct {
	entries []*models.LogEntry
	err     error
}

func (b *captureBackend) Send(entry *models.LogEntry) error {
	fields := make(map[string]interface{}, len(entry.Fields))
	for k, v := range entry.Fields {
		fields[k] = v
	}

	b.entries = append(b.entries, &models.LogEntry{
		Fields:     fields,
		LogLine:    append([]byte(nil), entry.LogLine...),
		Timestamp:  entry.Timestamp,
		SourcePath: entry.SourcePath,
		App:        entry.App,
	})
	return b.err
}

func (b *captureBackend) Shutdown()                     {}
func (b *captureBackend) Name() string                  { return "capture" }
func (b *captureBackend) CloseWriter(sourcePath string) {}

type timestampStage struct {
	ts time.Time
}

func (s *timestampStage) Name() string { return "timestamp_stage" }

func (s *timestampStage) Process(entry *models.LogEntry) (bool, error) {
	entry.Timestamp = s.ts
	return true, nil
}

// Process runs a log entry through the configured stages.
func (p *testPipeline) Process(entry *models.LogEntry) bool {
	for _, stage := range p.stages {
		keep, err := stage.Process(entry)
		if err != nil {
			// In a real pipeline manager, this error would be logged.
			// For testing, we can consider it a failure.
			return false
		}
		if !keep {
			return false // A stage decided to drop the log.
		}
	}
	return true // Keep the log if no stage drops it.
}

// setupTest creates a temporary directory and a FileBackend for testing.
func setupTest(t *testing.T) (string, backends.Backend, string) {
	t.Helper()
	tempDir := t.TempDir()
	sourceLogPath := filepath.Join(tempDir, "test.log")
	require.NoError(t, state.Initialize(filepath.Join(tempDir, "state.json")))
	backend := backends.NewFileBackend(".enriched_test")
	return sourceLogPath, backend, sourceLogPath + ".enriched_test"
}

func teardownTest(t *testing.T, sourcePath string, backend backends.Backend) {
	t.Helper()
	backend.CloseWriter(sourcePath)
}

func TestLogProcessor_ProcessLine_Success(t *testing.T) {
	// Arrange
	sourcePath, backend, enrichedPath := setupTest(t)
	defer teardownTest(t, sourcePath, backend)
	pl := &testPipeline{stages: []pipeline.Stage{pipeline.NewJSONParser()}}
	processor := NewLogProcessor("testApp", sourcePath, pl, backend)

	line := []byte(`{"message":"hello","level":"info"}`)

	// Act
	err := processor.ProcessLine(line)

	// Assert
	require.NoError(t, err)

	content, err := os.ReadFile(enrichedPath)
	require.NoError(t, err, "Enriched log file should be created and readable")

	var result map[string]any
	err = json.Unmarshal(content, &result)
	require.NoError(t, err, "Enriched log should be valid JSON")

	assert.Equal(t, "hello", result["message"])
	assert.Equal(t, "info", result["level"])
}

func TestLogProcessor_ProcessLine_DroppedByPipeline(t *testing.T) {
	// Arrange
	sourcePath, backend, enrichedPath := setupTest(t)
	defer teardownTest(t, sourcePath, backend)
	filterParams := map[string]interface{}{"action": "drop", "regex": "debug"}
	filterStage, err := pipeline.NewFilterStage(filterParams)
	require.NoError(t, err)

	pl := &testPipeline{stages: []pipeline.Stage{filterStage}}
	processor := NewLogProcessor("testApp", sourcePath, pl, backend)

	line := []byte(`{"message":"this is a debug message"}`)

	// Act
	err = processor.ProcessLine(line)

	// Assert
	require.NoError(t, err)

	_, err = os.Stat(enrichedPath)
	assert.True(t, os.IsNotExist(err), "Enriched log file should not be created for dropped lines")
}

func TestLogProcessor_ProcessLine_NoParserMatches(t *testing.T) {
	// Arrange
	sourcePath, backend, enrichedPath := setupTest(t)
	defer teardownTest(t, sourcePath, backend)
	pl := &testPipeline{stages: []pipeline.Stage{}}
	processor := NewLogProcessor("testApp", sourcePath, pl, backend)

	line := []byte(`this is not json`)

	// Act
	err := processor.ProcessLine(line)

	// Assert
	require.NoError(t, err)
	content, err := os.ReadFile(enrichedPath)
	require.NoError(t, err)

	require.NoError(t, err)
	assert.Equal(t, string(line)+"\n", string(content))
}

func TestLogProcessor_ProcessLine_SetsMetadataAndFallbackTimestamp(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.log")
	backend := &captureBackend{}
	pl := &testPipeline{}
	processor := NewLogProcessor("orders-api", sourcePath, pl, backend)

	start := time.Now()
	err := processor.ProcessLine([]byte("plain-text"))
	end := time.Now()

	require.NoError(t, err)
	require.Len(t, backend.entries, 1)

	entry := backend.entries[0]
	assert.Equal(t, sourcePath, entry.SourcePath)
	assert.Equal(t, "orders-api", entry.App)
	assert.Equal(t, "plain-text", string(entry.LogLine))
	assert.False(t, entry.Timestamp.Before(start))
	assert.False(t, entry.Timestamp.After(end.Add(500*time.Millisecond)))
}

func TestLogProcessor_ProcessLine_PreservesTimestampFromPipeline(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.log")
	backend := &captureBackend{}
	fixed := time.Date(2025, time.January, 10, 11, 12, 13, 0, time.UTC)
	pl := &testPipeline{
		stages: []pipeline.Stage{
			&timestampStage{ts: fixed},
		},
	}
	processor := NewLogProcessor("orders-api", sourcePath, pl, backend)

	err := processor.ProcessLine([]byte(`{"msg":"ok"}`))

	require.NoError(t, err)
	require.Len(t, backend.entries, 1)
	assert.Equal(t, fixed, backend.entries[0].Timestamp)
}

func TestLogProcessor_ProcessLineWithTimestamp_UsesProvidedTimestamp(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.log")
	backend := &captureBackend{}
	pl := &testPipeline{}
	processor := NewLogProcessor("orders-api", sourcePath, pl, backend)
	provided := time.Date(2025, time.February, 20, 10, 11, 12, 123, time.UTC)

	err := processor.ProcessLineWithTimestamp([]byte("line"), provided)

	require.NoError(t, err)
	require.Len(t, backend.entries, 1)
	assert.Equal(t, provided, backend.entries[0].Timestamp)
}

func TestLogProcessor_ProcessLineWithTimestamp_PipelineCanOverrideTimestamp(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.log")
	backend := &captureBackend{}
	provided := time.Date(2025, time.February, 20, 10, 11, 12, 123, time.UTC)
	overridden := time.Date(2026, time.March, 21, 1, 2, 3, 0, time.UTC)
	pl := &testPipeline{
		stages: []pipeline.Stage{
			&timestampStage{ts: overridden},
		},
	}
	processor := NewLogProcessor("orders-api", sourcePath, pl, backend)

	err := processor.ProcessLineWithTimestamp([]byte("line"), provided)

	require.NoError(t, err)
	require.Len(t, backend.entries, 1)
	assert.Equal(t, overridden, backend.entries[0].Timestamp)
}

func TestLogProcessor_ProcessLine_PropagatesBackendErrors(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.log")
	expectedErr := errors.New("backend write failed")
	backend := &captureBackend{err: expectedErr}
	pl := &testPipeline{}
	processor := NewLogProcessor("orders-api", sourcePath, pl, backend)

	err := processor.ProcessLine([]byte("line"))

	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
	require.Len(t, backend.entries, 1)
}
