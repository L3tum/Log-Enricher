package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"log-enricher/internal/config"

	"github.com/goccy/go-json"
)

func TestRunApplicationWithFileBackend(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "log-enricher-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir) // Clean up after the test

	t.Logf("Using temporary directory: %s", tempDir)

	// Define paths within the temporary directory
	stateFilePath := filepath.Join(tempDir, "state.json")
	logFilePath := filepath.Join(tempDir, "test.log")
	enrichedFileSuffix := ".enriched"
	enrichedLogFilePath := logFilePath + enrichedFileSuffix

	// Create a dummy log file
	err = os.MkdirAll(filepath.Dir(logFilePath), 0755)
	if err != nil {
		t.Fatalf("Failed to create log file directory: %v", err)
	}
	logFile, err := os.Create(logFilePath)
	if err != nil {
		t.Fatalf("Failed to create log file: %v", err)
	}
	logFile.Close() // Close immediately, watcher will open it

	// Configure the application for the test
	cfg := &config.Config{
		LogLevel:           "DEBUG",
		Backend:            "file",
		StateFilePath:      stateFilePath,
		LogBasePath:        tempDir,
		EnrichedFileSuffix: enrichedFileSuffix,
		AppName:            "test",
		LogFileExtensions:  []string{".log"},
		// Other fields can be left at their defaults or set as needed
	}

	t.Log("Starting application...")

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		if appErr := runApplication(ctx, cfg); appErr != nil {
			t.Errorf("runApplication failed: %v", appErr)
		}
	}()

	t.Log("Starting test...")

	// Give the watcher a moment to start
	time.Sleep(500 * time.Millisecond)

	// Record initial memory usage
	initialMemUsageMB := getMemUsageMB()
	t.Logf("Initial memory usage (Alloc): %d MB", initialMemUsageMB)

	// Open the log file once for all writes
	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open log file for writing: %v", err)
	}
	defer f.Close() // Ensure the file is closed at the end of the test

	numLogLines := 1000
	testLogLines := make([]string, numLogLines)
	for i := 0; i < numLogLines; i++ {
		testLogLines[i] = fmt.Sprintf(`{"level":"info","msg":"User logged in","user_id":"%d","ip":"192.168.1.%d"}`, i, i%255)
		_, err = fmt.Fprintln(f, testLogLines[i])
		if err != nil {
			t.Fatalf("Failed to write to log file: %v", err)
		}
	}

	// Give the pipeline some time to process the logs
	// Increased sleep duration for more lines
	time.Sleep(5 * time.Second)

	// Record final memory usage
	finalMemUsageMB := getMemUsageMB()
	t.Logf("Final memory usage (Alloc): %d MB", finalMemUsageMB)

	const maxMemoryIncreaseMB = 2 // Allow up to 2 MB increase for 1000 lines
	if finalMemUsageMB > initialMemUsageMB+maxMemoryIncreaseMB {
		t.Errorf("Memory usage ballooned: initial %d MB, final %d MB, increase %d MB (max allowed %d MB)",
			initialMemUsageMB, finalMemUsageMB, finalMemUsageMB-initialMemUsageMB, maxMemoryIncreaseMB)
	}

	// Trigger graceful shutdown
	cancel()
	wg.Wait() // Wait for runApplication to finish

	// Check if the enriched file exists
	_, err = os.Stat(enrichedLogFilePath)
	if os.IsNotExist(err) {
		t.Fatalf("Enriched log file not found at %s", enrichedLogFilePath)
	} else if err != nil {
		t.Fatalf("Error checking enriched log file: %v", err)
	}

	// Read the content of the enriched file
	enrichedContentBytes, err := os.ReadFile(enrichedLogFilePath)
	if err != nil {
		t.Fatalf("Failed to read enriched log file: %v", err)
	}
	enrichedContent := string(enrichedContentBytes)

	// Adjust test for order-agnostic comparison
	enrichedLines := strings.Split(strings.TrimSpace(enrichedContent), "\n")
	if len(enrichedLines) == 0 || (len(enrichedLines) == 1 && enrichedLines[0] == "") {
		t.Fatalf("No enriched log lines found in %s", enrichedLogFilePath)
	}

	if len(enrichedLines) != numLogLines {
		t.Errorf("Expected %d enriched log lines, got %d", numLogLines, len(enrichedLines))
	}

	var enrichedMaps []map[string]interface{}
	for i, line := range enrichedLines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("Failed to unmarshal enriched line %d: %v, content: %s", i, err, line)
			continue
		}
		enrichedMaps = append(enrichedMaps, m)
	}

	for _, expectedLine := range testLogLines {
		var expectedMap map[string]interface{}
		if err := json.Unmarshal([]byte(expectedLine), &expectedMap); err != nil {
			t.Fatalf("Failed to unmarshal expected line: %v, content: %s", err, expectedLine)
		}

		foundMatch := false
		for _, actualMap := range enrichedMaps {
			if mapsContain(expectedMap, actualMap) {
				foundMatch = true
				break
			}
		}

		if !foundMatch {
			t.Errorf("Enriched content missing expected log line (order-agnostic check): %s\nFull enriched content:\n%s", expectedLine, enrichedContent)
		}
	}

	t.Logf("Enriched log file content:\n%s", enrichedContent)
}

// getMemUsageMB returns the current heap allocation in MB after forcing a garbage collection.
func getMemUsageMB() uint64 {
	var m runtime.MemStats
	runtime.GC() // Force garbage collection to get a more accurate live memory usage
	runtime.ReadMemStats(&m)
	return m.Alloc / 1024 / 1024 // Convert bytes to MB
}

// mapsContain checks if all key-value pairs in 'expected' are present and equal in 'actual'.
// This allows 'actual' to have additional fields (from enrichment).
func mapsContain(expected, actual map[string]interface{}) bool {
	for k, v := range expected {
		actualVal, ok := actual[k]
		if !ok {
			return false // Key not found in actual
		}
		// Simple comparison for basic types. For nested maps/slices, a recursive comparison
		// or a more robust deep equality check would be needed.
		if fmt.Sprintf("%v", v) != fmt.Sprintf("%v", actualVal) {
			return false // Value mismatch
		}
	}
	return true
}
