package logging

import (
	"bytes"
	"io"
	"log"
	"os"
	"sync"
	"testing"
)

// Mock Backend Manager for logging test
type mockLogBackendManager struct {
	mu            sync.Mutex
	lastBroadcast map[string]interface{}
}

func (m *mockLogBackendManager) Broadcast(sourcePath string, entry map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastBroadcast = entry
}
func (m *mockLogBackendManager) Shutdown() {}

func TestDualLogger(t *testing.T) {
	// 1. Setup mocks
	originalStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
		log.SetOutput(os.Stderr)
	}()

	mockBackends := &mockLogBackendManager{}

	// 2. Initialize our custom logger
	New(mockBackends)

	// 3. Log a message
	log.Println("test message")
	w.Close()

	// 4. Verify stdout output
	var stdoutBuf bytes.Buffer
	_, _ = io.Copy(&stdoutBuf, r)
	if !bytes.Contains(stdoutBuf.Bytes(), []byte("test message")) {
		t.Errorf("stdout did not contain the expected message. Got: %q", stdoutBuf.String())
	}

	// 5. Verify backend output
	mockBackends.mu.Lock()
	defer mockBackends.mu.Unlock()
	if mockBackends.lastBroadcast == nil {
		t.Fatal("backend manager did not receive a broadcast")
	}
	if msg, ok := mockBackends.lastBroadcast["message"]; !ok || msg != "test message" {
		t.Errorf("backend received incorrect message: %v", msg)
	}
}
