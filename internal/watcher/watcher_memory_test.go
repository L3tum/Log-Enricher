package watcher_test

import (
	"context"
	_ "log-enricher/internal/backends"
	"log-enricher/internal/bufferpool"
	"log-enricher/internal/cache"
	"log-enricher/internal/config"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/state"
	"log-enricher/internal/watcher"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// --- Mock Implementations for Testing ---

// mockPipelineManager implements pipeline.Manager
type mockPipelineManager struct{}

func (m *mockPipelineManager) Process(line []byte, entry *bufferpool.LogEntry) bool {
	// Simulate some processing, but don't allocate new memory
	entry.Fields["processed"] = true
	return false // Don't drop the line
}

func (m *mockPipelineManager) EnrichmentStages() []pipeline.EnrichmentStage {
	return nil // No actual enrichment stages for this test
}

// mockBackendManager implements backends.Manager
type mockBackendManager struct {
	wg sync.WaitGroup // Add WaitGroup for synchronization
}

func (m *mockBackendManager) Broadcast(source string, entry bufferpool.LogEntry) {
	// Do nothing, just simulate sending
	m.wg.Done() // Signal that one broadcast has occurred
}

func (m *mockBackendManager) Shutdown() {
	// Do nothing
}

// Wait waits until the expected number of broadcasts have been received.
// The caller is responsible for calling Add() on the WaitGroup.
func (m *mockBackendManager) Wait() {
	m.wg.Wait() // Wait until all expected broadcasts have called Done()
}

// --- Test Function ---

func TestLogWatcherMemoryUsage(t *testing.T) {
	// Use a temporary directory for log files and state
	tempDir, err := os.MkdirTemp("", "log-enricher-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir) // Clean up temp dir after test

	logFilePath := filepath.Join(tempDir, "test.log")
	stateFilePath := filepath.Join(tempDir, "state.json")
	_ = filepath.Join(tempDir, "cache.json") // Cache is part of state, but good to be explicit

	// Initialize state and cache (dependencies of LogWatcher)
	if err := state.Initialize(stateFilePath); err != nil {
		t.Fatalf("Failed to initialize state: %v", err)
	}
	if err := cache.Initialize(10000); err != nil { // Reasonable cache size
		t.Fatalf("Failed to initialize cache: %v", err)
	}

	// Create a dummy config
	cfg := &config.Config{
		LogBasePath:                tempDir,
		LogFileExtensions:          []string{".log"},
		StateFilePath:              stateFilePath,
		CacheSize:                  10000,
		ParseTimestampEnabled:      true,
		TimestampFields:            []string{"timestamp"},
		PlaintextProcessingEnabled: true,
	}

	mockPipeline := &mockPipelineManager{}
	mockBackends := &mockBackendManager{} // Use the updated mockBackendManager

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the LogWatcher
	err = watcher.StartLogWatcher(ctx, cfg, mockPipeline, mockBackends)
	if err != nil {
		t.Fatalf("Failed to start log watcher: %v", err)
	}

	numLines := 100000 // Number of lines to write in each batch
	lineContent := `{"timestamp":"2023-01-01T12:00:00Z", "level":"info", "message":"This is a test log line with some content to simulate real data."}` + "\n"

	// --- Batch 1: Initial processing ---
	t.Logf("Writing %d initial log lines...", numLines)
	mockBackends.wg.Add(numLines) // Add expected count for Batch 1
	writeLogLines(t, logFilePath, numLines, lineContent)

	// Wait for the watcher to process and broadcast all lines from Batch 1
	mockBackends.Wait()
	t.Logf("Finished processing %d initial log lines.", numLines)

	// --- Measure memory after initial processing ---
	runtime.GC() // Force garbage collection
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)
	t.Logf("Memory after initial %d lines: HeapAlloc = %d bytes, Sys = %d bytes",
		numLines, memStatsBefore.HeapAlloc, memStatsBefore.Sys)

	// --- Batch 2: Continuous processing ---
	t.Logf("Writing another %d log lines...", numLines)
	mockBackends.wg.Add(numLines) // Add expected count for Batch 2
	writeLogLines(t, logFilePath, numLines, lineContent)

	// Wait for the watcher to process and broadcast all lines from Batch 2
	mockBackends.Wait() // Wait for the additional numLines
	t.Logf("Finished processing total %d log lines.", 2*numLines)

	// --- Measure memory after continuous processing ---
	runtime.GC() // Force garbage collection
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)
	t.Logf("Memory after total %d lines: HeapAlloc = %d bytes, Sys = %d bytes",
		2*numLines, memStatsAfter.HeapAlloc, memStatsAfter.Sys)

	// Assert that HeapAlloc hasn't grown excessively
	// Allow for a small, constant increase due to internal structures, but not linear growth.
	// A 10MB increase is a generous threshold for 100k lines of small JSON.
	// The key is that it should not double or grow proportionally to the second batch.
	maxAllowedIncreaseMB := 10.0
	increaseBytes := memStatsAfter.HeapAlloc - memStatsBefore.HeapAlloc
	increaseMB := float64(increaseBytes) / (1024 * 1024)

	t.Logf("HeapAlloc increase: %d bytes (%.2f MB)", increaseBytes, increaseMB)

	if increaseMB > maxAllowedIncreaseMB {
		t.Errorf("Memory leak detected: HeapAlloc increased by %.2f MB, expected less than %.2f MB",
			increaseMB, maxAllowedIncreaseMB)
	} else {
		t.Logf("Memory usage appears stable. HeapAlloc increase: %.2f MB (within %.2f MB limit)",
			increaseMB, maxAllowedIncreaseMB)
	}

	// Clean up state and cache
	if err := cache.SaveToState(); err != nil {
		t.Errorf("Error saving cache to state: %v", err)
	}
	if err := state.Save(cfg.StateFilePath); err != nil {
		t.Errorf("Error saving state: %v", err)
	}
}

// Helper to write log lines to a file
func writeLogLines(t *testing.T, filePath string, count int, content string) {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open log file %s: %v", filePath, err)
	}
	defer f.Close()

	for i := 0; i < count; i++ {
		_, err := f.WriteString(content)
		if err != nil {
			t.Fatalf("Failed to write to log file %s: %v", filePath, err)
		}
	}
}
