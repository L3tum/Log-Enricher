package processor

import (
	"encoding/json"
	"log-enricher/internal/backends"
	"log-enricher/internal/models"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/state"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
