package logging

import (
	"bytes"
	"io"
	"log"
	"log-enricher/internal/bufferpool"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBackendManager is a mock implementation of the backends.Manager.
type mockBackendManager struct {
	lastSource string
	lastInput  map[string]interface{}
	callCount  int
}

// Broadcast records the call and its arguments for later assertion.
func (m *mockBackendManager) Broadcast(source string, entry bufferpool.LogEntry) {
	m.lastSource = source

	// Make a copy of the Fields map to avoid issues with pooling.
	m.lastInput = make(map[string]interface{}, len(entry.Fields))
	for k, v := range entry.Fields {
		m.lastInput[k] = v
	}

	m.callCount++
}

func (m *mockBackendManager) Shutdown() {}

// captureOutput captures everything written to os.Stdout during the execution of a function.
func captureOutput(f func()) string {
	var mu sync.Mutex
	mu.Lock()
	defer mu.Unlock()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestDualLogger(t *testing.T) {
	// Save the original logger state to restore it after the test.
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// Arrange
	mockManager := &mockBackendManager{}
	testMessage := "hello world"

	// Act: Call a standard log function, which should be intercepted by our dualLogger.
	output := captureOutput(func() {
		// Step 2: Initialize the logger *inside* the capture function.
		// This ensures it uses the redirected stdout pipe.
		New(mockManager)
		log.Println(testMessage)
	})

	// Assert: Check that the log was written to stdout.
	assert.True(t, strings.HasSuffix(output, testMessage+"\n"), "Expected log message to be written to stdout")

	// Assert: Check that the log was broadcast to the backend manager.
	require.Equal(t, 1, mockManager.callCount, "Broadcast should have been called once")
	assert.Equal(t, "internal", mockManager.lastSource, "Broadcast source should be 'internal'")

	// Assert: Check the structure of the broadcasted log entry.
	expectedEntry := map[string]interface{}{
		"level":   "info",
		"message": testMessage,
		"source":  "internal",
	}
	assert.Equal(t, expectedEntry, mockManager.lastInput, "Broadcast entry has incorrect content")
}
