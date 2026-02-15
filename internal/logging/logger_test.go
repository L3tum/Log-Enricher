package logging

import (
	"bytes"
	"io"
	"log-enricher/internal/models"
	"log/slog" // Import slog
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBackend is a mock implementation of the backends.Backend interface.
type mockBackend struct {
	mu         sync.Mutex
	lastSource string
	lastEntry  *models.LogEntry
	callCount  int
	sendError  error // Error to return from Send
}

func (m *mockBackend) Name() string {
	return "mock"
}

// Send records the call and its arguments for later assertion.
func (m *mockBackend) Send(entry *models.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastSource = entry.SourcePath
	// Make a deep copy of the LogEntry to avoid issues with pooling or subsequent modifications.
	copiedEntry := &models.LogEntry{
		Timestamp: entry.Timestamp,
		App:       entry.App,
		Fields:    make(map[string]interface{}, len(entry.Fields)),
	}
	for k, v := range entry.Fields {
		copiedEntry.Fields[k] = v
	}
	m.lastEntry = copiedEntry
	m.callCount++
	return m.sendError
}

func (m *mockBackend) Shutdown() {}

func (m *mockBackend) CloseWriter(sourcePath string) {}

// captureOutput captures everything written to os.Stdout during the execution of a function.
func captureOutput(f func()) string {
	var mu sync.Mutex
	mu.Lock()
	defer mu.Unlock()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestSlogIntegration(t *testing.T) {
	// Save the original default logger to restore it after the test.
	originalLogger := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})

	// Arrange
	mockBackend := &mockBackend{}
	testMessage := "hello world"
	testAttrKey := "user_id"
	testAttrValue := 123

	// Act: Initialize the logger and call an info log function using slog.
	output := captureOutput(func() {
		// Fix: Added "internal" as the sourcePath argument.
		New("INFO", "internal", mockBackend) // Set minLevel to INFO
		slog.Info(testMessage, testAttrKey, testAttrValue)
	})

	// Assert: Check that the log was written to stdout with the correct prefix and attributes.
	assert.True(t, strings.Contains(output, "level=INFO msg=\"hello world\" user_id=123\n"), "Expected log message to be written to stdout by slog.NewTextHandler")

	// Assert: Check that the log was sent to the backend.
	require.Equal(t, 1, mockBackend.callCount, "Backend Send should have been called once")
	assert.Equal(t, "internal", mockBackend.lastSource, "Backend source should be 'internal'")

	// Assert: Check the structure of the sent log entry.
	expectedFields := map[string]interface{}{
		"level":     "INFO",
		"message":   testMessage,
		"source":    "internal",
		testAttrKey: int64(testAttrValue), // JSON unmarshaling might convert int to float64
	}
	assert.Equal(t, expectedFields, mockBackend.lastEntry.Fields, "Backend entry has incorrect content")
	assert.Equal(t, "log-enricher", mockBackend.lastEntry.App, "Backend entry has incorrect app name")
	assert.WithinDuration(t, time.Now(), mockBackend.lastEntry.Timestamp, 5*time.Second, "Timestamp should be recent")
}

func TestSlogLevelFiltering(t *testing.T) {
	tests := []struct {
		name          string
		configLevel   string
		logLevel      slog.Level
		expectBackend bool
	}{
		{"Debug with INFO config", "INFO", slog.LevelDebug, false},
		{"Info with INFO config", "INFO", slog.LevelInfo, true},
		{"Warn with INFO config", "INFO", slog.LevelWarn, true},
		{"Error with INFO config", "INFO", slog.LevelError, true},
		{"Debug with DEBUG config", "DEBUG", slog.LevelDebug, true},
		{"Info with DEBUG config", "DEBUG", slog.LevelInfo, true},
		{"Warn with DEBUG config", "DEBUG", slog.LevelWarn, true},
		{"Error with DEBUG config", "DEBUG", slog.LevelError, true},
		{"Info with ERROR config", "ERROR", slog.LevelInfo, false},
		{"Error with ERROR config", "ERROR", slog.LevelError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Restore default logger for each test run
			originalLogger := slog.Default()
			defer slog.SetDefault(originalLogger)

			mockBackend := &mockBackend{}
			// Fix: Added "internal" as the sourcePath argument.
			New(tt.configLevel, "internal", mockBackend)
			testMessage := "test message"

			_ = captureOutput(func() {
				switch tt.logLevel {
				case slog.LevelDebug:
					slog.Debug(testMessage)
				case slog.LevelInfo:
					slog.Info(testMessage)
				case slog.LevelWarn:
					slog.Warn(testMessage)
				case slog.LevelError:
					slog.Error(testMessage)
				}
			})

			if tt.expectBackend {
				require.Equal(t, 1, mockBackend.callCount, "Backend Send should have been called")
				assert.Equal(t, strings.ToUpper(tt.logLevel.String()), mockBackend.lastEntry.Fields["level"], "Backend entry level mismatch")
			} else {
				assert.Equal(t, 0, mockBackend.callCount, "Backend Send should NOT have been called")
			}
		})
	}
}
