package watcher

import (
	"log-enricher/internal/bufferpool"
	"log-enricher/internal/config"
	"log-enricher/internal/pipeline"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPipelineManager is a mock implementation of the pipeline.Manager.
type mockPipelineManager struct {
	// inputReceived stores the log entry that was passed to the Process method.
	inputReceived map[string]interface{}
	// outputToReturn is the enriched entry that the mock will return.
	outputToReturn map[string]interface{}
	// dropSignal is the boolean that the mock will return.
	dropSignal bool
}

// 1. Modify mockPipelineManager.Process to accept *bufferpool.LogEntry and update its internal inputReceived field.
func (m *mockPipelineManager) Process(_ []byte, logEntry *bufferpool.LogEntry) bool {
	// Make a copy of the map to avoid issues with the logEntryPool.
	// The original map will be cleared when returned to the pool.
	m.inputReceived = make(map[string]interface{}, len(logEntry.Fields))
	for k, v := range logEntry.Fields {
		m.inputReceived[k] = v
	}
	// Apply the mock output to the logEntry's Fields
	for k, v := range m.outputToReturn {
		logEntry.Fields[k] = v
	}
	return m.dropSignal
}

func (m *mockPipelineManager) EnrichmentStages() []pipeline.EnrichmentStage {
	return nil
}

// Mock Backend Manager
type mockBackendManager struct {
	mu            sync.Mutex
	lastBroadcast bufferpool.LogEntry // Capture the entire LogEntry
	callCount     int
}

// 2. Modify mockBackendManager.Broadcast to accept *bufferpool.LogEntry and update its internal lastBroadcast field.
func (m *mockBackendManager) Broadcast(sourcePath string, entry bufferpool.LogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Make a deep copy of the LogEntry to avoid issues with pooling.
	// This includes copying the Fields map and the Timestamp.
	m.lastBroadcast = bufferpool.LogEntry{
		Fields:    make(map[string]interface{}, len(entry.Fields)),
		Timestamp: entry.Timestamp,
	}
	for k, v := range entry.Fields {
		m.lastBroadcast.Fields[k] = v
	}
	m.callCount++
}
func (m *mockBackendManager) Shutdown() {}

func TestProcessLine(t *testing.T) {
	testCases := []struct {
		name                         string
		inputLine                    string
		plaintextEnabled             bool
		parseTimestampEnabled        bool
		timestampFields              []string
		expectedInputForPipeline     map[string]interface{}
		pipelineWillDrop             bool
		pipelineOutput               map[string]interface{}
		expectBroadcast              bool
		expectedBroadcastFromBackend map[string]interface{}
		// New fields for timestamp assertion
		expectTimestampToBeNow bool
		expectedFixedTimestamp time.Time
	}{
		{
			name:                         "Plain text with IP",
			inputLine:                    "Connection from 8.8.8.8",
			plaintextEnabled:             true,
			parseTimestampEnabled:        false,
			expectedInputForPipeline:     map[string]interface{}{"message": "Connection from 8.8.8.8"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"message": "Connection from 8.8.8.8", "client_hostname": "dns.google"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"message": "Connection from 8.8.8.8", "client_hostname": "dns.google"},
			expectTimestampToBeNow:       true, // Expect current time
		},
		{
			name:                         "JSON with client_ip field",
			inputLine:                    `{"client_ip": "8.8.8.8", "user": "test"}`,
			plaintextEnabled:             true,
			parseTimestampEnabled:        false,
			expectedInputForPipeline:     map[string]interface{}{"client_ip": "8.8.8.8", "user": "test"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"client_ip": "8.8.8.8", "user": "test", "client_hostname": "dns.google"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"client_ip": "8.8.8.8", "user": "test", "client_hostname": "dns.google"},
			expectTimestampToBeNow:       true, // Expect current time
		},
		{
			name:             "Plain text processing disabled",
			inputLine:        "some plain text",
			plaintextEnabled: false,
			// The pipeline should not be called.
			expectedInputForPipeline: nil,
			// It should be broadcast directly, bypassing the pipeline.
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"message": "some plain text"},
			expectTimestampToBeNow:       true, // Expect current time
		},
		{
			name:                     "Pipeline drops the log",
			inputLine:                `{"message": "debug info"}`,
			plaintextEnabled:         true,
			parseTimestampEnabled:    false,
			expectedInputForPipeline: map[string]interface{}{"message": "debug info"},
			pipelineWillDrop:         true, // The pipeline decides to drop this.
			pipelineOutput:           map[string]interface{}{"message": "debug info"},
			expectBroadcast:          false, // No broadcast should happen.
			expectTimestampToBeNow:   true,  // Expect current time
		},
		{
			name:                         "JSON with timestamp field",
			inputLine:                    `{"time": "2023-01-01T12:34:56Z", "message": "event"}`,
			plaintextEnabled:             true,
			parseTimestampEnabled:        true,
			timestampFields:              []string{"time"},
			expectedInputForPipeline:     map[string]interface{}{"time": "2023-01-01T12:34:56Z", "message": "event"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"time": "2023-01-01T12:34:56Z", "message": "event"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"time": "2023-01-01T12:34:56Z", "message": "event"},
			expectedFixedTimestamp:       time.Date(2023, time.January, 1, 12, 34, 56, 0, time.UTC),
		},
		{
			name:                         "JSON with different timestamp field and format",
			inputLine:                    `{"log_timestamp": "2023-03-15 10:00:00.123", "level": "INFO"}`,
			plaintextEnabled:             true,
			parseTimestampEnabled:        true,
			timestampFields:              []string{"log_timestamp"},
			expectedInputForPipeline:     map[string]interface{}{"log_timestamp": "2023-03-15 10:00:00.123", "level": "INFO"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"log_timestamp": "2023-03-15 10:00:00.123", "level": "INFO"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"log_timestamp": "2023-03-15 10:00:00.123", "level": "INFO"},
			expectedFixedTimestamp:       time.Date(2023, time.March, 15, 10, 0, 0, 123000000, time.UTC),
		},
		{
			name:                         "JSON with unparseable timestamp",
			inputLine:                    `{"ts": "not-a-timestamp", "message": "bad log"}`,
			plaintextEnabled:             true,
			parseTimestampEnabled:        true,
			timestampFields:              []string{"ts"},
			expectedInputForPipeline:     map[string]interface{}{"ts": "not-a-timestamp", "message": "bad log"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"ts": "not-a-timestamp", "message": "bad log"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"ts": "not-a-timestamp", "message": "bad log"},
			expectTimestampToBeNow:       true, // Parsing fails, so it falls back to time.Now()
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			mockPipeline := &mockPipelineManager{
				dropSignal:     tc.pipelineWillDrop,
				outputToReturn: tc.pipelineOutput,
			}
			mockBackends := &mockBackendManager{}
			lw := &LogWatcher{
				cfg: &config.Config{
					PlaintextProcessingEnabled: tc.plaintextEnabled,
					ParseTimestampEnabled:      tc.parseTimestampEnabled,
					TimestampFields:            tc.timestampFields,
				},
				manager:  mockPipeline,
				backends: mockBackends,
			}

			// Act
			var timeBeforeProcess time.Time
			if tc.expectTimestampToBeNow {
				timeBeforeProcess = time.Now()
			}

			// Acquire a buffer from the pool for the test
			pooledBuffer := bufferpool.GetByteBuffer()
			pooledBuffer.WriteString(tc.inputLine)

			lw.processLine("test.log", pooledBuffer) // Pass the pooled buffer

			// Assert
			assert.Equal(t, tc.expectedInputForPipeline, mockPipeline.inputReceived, "The input received by the pipeline manager was not as expected.")

			if tc.expectBroadcast {
				require.Equal(t, 1, mockBackends.callCount, "Expected the backend to receive exactly one broadcast")
				assert.Equal(t, tc.expectedBroadcastFromBackend, mockBackends.lastBroadcast.Fields, "The broadcasted entry fields are not what was expected")

				// Assert the timestamp with appropriate logic
				if tc.expectTimestampToBeNow {
					// Allow a 1-minute delta for timestamps expected to be time.Now()
					assert.WithinDuration(t, timeBeforeProcess, mockBackends.lastBroadcast.Timestamp, 1*time.Minute,
						"Expected timestamp to be close to current time, got %v", mockBackends.lastBroadcast.Timestamp)
				} else if !tc.expectedFixedTimestamp.IsZero() {
					// For fixed timestamps, assert exact equality
					assert.True(t, tc.expectedFixedTimestamp.Equal(mockBackends.lastBroadcast.Timestamp),
						"Expected fixed timestamp %v, got %v", tc.expectedFixedTimestamp, mockBackends.lastBroadcast.Timestamp)
				} else {
					// This case should ideally not be reached if all timestamp expectations are covered.
					// If it is, it implies a zero timestamp is expected.
					assert.True(t, mockBackends.lastBroadcast.Timestamp.IsZero(), "Expected timestamp to be zero, but it was %v", mockBackends.lastBroadcast.Timestamp)
				}
			} else {
				assert.Equal(t, 0, mockBackends.callCount, "Expected the backend to not receive any broadcast")
			}
		})
	}
}

func TestGetMatchingLogFiles(t *testing.T) {
	// Arrange: Create a temporary directory structure
	tmpDir, err := os.MkdirTemp("", "watcher_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0755))

	expectedFiles := []string{
		filepath.Join(tmpDir, "a.log"),
		filepath.Join(tmpDir, "b.txt"),
		filepath.Join(subDir, "c.log"),
	}
	filesToCreate := append(expectedFiles, filepath.Join(tmpDir, "d.dat"))

	for _, f := range filesToCreate {
		_, err := os.Create(f)
		require.NoError(t, err)
	}

	// Act
	extensions := []string{".log", ".txt"}
	foundFiles, err := getMatchingLogFiles(tmpDir, extensions)
	require.NoError(t, err)

	// Assert
	// Sort both slices to ensure a consistent comparison
	sort.Strings(expectedFiles)
	sort.Strings(foundFiles)
	assert.Equal(t, expectedFiles, foundFiles, "The list of found files did not match the expected list")
}
