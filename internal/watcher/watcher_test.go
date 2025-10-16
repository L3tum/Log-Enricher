package watcher

import (
	"bytes"
	"log-enricher/internal/config"
	"log-enricher/internal/pipeline"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

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

func (m *mockPipelineManager) Process(_ []byte, logEntry map[string]interface{}) (bool, map[string]interface{}) {
	// Make a copy of the map to avoid issues with the logEntryPool.
	// The original map will be cleared when returned to the pool.
	m.inputReceived = make(map[string]interface{}, len(logEntry))
	for k, v := range logEntry {
		m.inputReceived[k] = v
	}
	return m.dropSignal, m.outputToReturn
}

func (m *mockPipelineManager) EnrichmentStages() []pipeline.EnrichmentStage {
	return nil
}

// Mock Backend Manager
type mockBackendManager struct {
	mu            sync.Mutex
	lastBroadcast map[string]interface{}
	callCount     int
}

func (m *mockBackendManager) Broadcast(sourcePath string, entry map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Make a copy of the map to avoid issues with pooling.
	m.lastBroadcast = make(map[string]interface{}, len(entry))
	for k, v := range entry {
		m.lastBroadcast[k] = v
	}
	m.callCount++
}
func (m *mockBackendManager) Shutdown() {}

func TestProcessLine(t *testing.T) {
	testCases := []struct {
		name                         string
		inputLine                    string
		plaintextEnabled             bool
		expectedInputForPipeline     map[string]interface{}
		pipelineWillDrop             bool
		pipelineOutput               map[string]interface{}
		expectBroadcast              bool
		expectedBroadcastFromBackend map[string]interface{}
	}{
		{
			name:                         "Plain text with IP",
			inputLine:                    "Connection from 8.8.8.8",
			plaintextEnabled:             true,
			expectedInputForPipeline:     map[string]interface{}{"message": "Connection from 8.8.8.8"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"message": "Connection from 8.8.8.8", "client_hostname": "dns.google"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"message": "Connection from 8.8.8.8", "client_hostname": "dns.google"},
		},
		{
			name:                         "JSON with client_ip field",
			inputLine:                    `{"client_ip": "8.8.8.8", "user": "test"}`,
			plaintextEnabled:             true,
			expectedInputForPipeline:     map[string]interface{}{"client_ip": "8.8.8.8", "user": "test"},
			pipelineWillDrop:             false,
			pipelineOutput:               map[string]interface{}{"client_ip": "8.8.8.8", "user": "test", "client_hostname": "dns.google"},
			expectBroadcast:              true,
			expectedBroadcastFromBackend: map[string]interface{}{"client_ip": "8.8.8.8", "user": "test", "client_hostname": "dns.google"},
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
		},
		{
			name:                     "Pipeline drops the log",
			inputLine:                `{"message": "debug info"}`,
			plaintextEnabled:         true,
			expectedInputForPipeline: map[string]interface{}{"message": "debug info"},
			pipelineWillDrop:         true, // The pipeline decides to drop this.
			pipelineOutput:           map[string]interface{}{"message": "debug info"},
			expectBroadcast:          false, // No broadcast should happen.
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			pipeline := &mockPipelineManager{
				dropSignal:     tc.pipelineWillDrop,
				outputToReturn: tc.pipelineOutput,
			}
			backends := &mockBackendManager{}
			lw := &LogWatcher{
				cfg: &config.Config{
					PlaintextProcessingEnabled: tc.plaintextEnabled,
				},
				manager:  pipeline,
				backends: backends,
			}

			// Act
			lw.processLine("test.log", bytes.NewBufferString(tc.inputLine))

			// Assert
			assert.Equal(t, tc.expectedInputForPipeline, pipeline.inputReceived, "The input received by the pipeline manager was not as expected.")

			if tc.expectBroadcast {
				require.Equal(t, 1, backends.callCount, "Expected the backend to receive exactly one broadcast")
				assert.Equal(t, tc.expectedBroadcastFromBackend, backends.lastBroadcast, "The broadcasted entry is not what was expected")
			} else {
				assert.Equal(t, 0, backends.callCount, "Expected the backend to not receive any broadcast")
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
