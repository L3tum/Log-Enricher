package state

import (
	"log-enricher/internal/models"
	"os"
	"path/filepath"
	"testing"
)

func setupTestState(t *testing.T) (string, func()) {
	// Create a temporary directory for the test.
	tmpDir, err := os.MkdirTemp("", "state_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// The cleanup function to be called by the test.
	cleanup := func() {
		os.RemoveAll(tmpDir)
		// Reset the global state after each test.
		globalState = nil
	}

	return tmpDir, cleanup
}

func TestSaveAndLoadState(t *testing.T) {
	tmpDir, cleanup := setupTestState(t)
	defer cleanup()

	stateFilePath := filepath.Join(tmpDir, "state.json")
	logFilePath := filepath.Join(tmpDir, "app.log")

	// Create a dummy log file so stat works during save.
	if err := os.WriteFile(logFilePath, []byte("dummy line"), 0644); err != nil {
		t.Fatalf("Failed to create dummy log file: %v", err)
	}

	// 1. Initialize and populate a state.
	if err := Initialize(stateFilePath); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	fs := GetOrCreateFileState(logFilePath)
	fs.LineNumber = 100
	fs.LineContent = "last line content"

	SetCacheEntry("1.1.1.1", models.Result{Hostname: "one.one.one.one", Found: true})

	// 2. Save the state.
	if err := Save(stateFilePath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// 3. Re-initialize, which will load the saved state.
	if err := Initialize(stateFilePath); err != nil {
		t.Fatalf("Initialize (for loading) failed: %v", err)
	}

	// 4. Verify the loaded state.
	if GetCacheSize() != 1 {
		t.Errorf("expected cache size of 1, got %d", GetCacheSize())
	}
	res, ok := GetCacheEntry("1.1.1.1")
	if !ok || res.Hostname != "one.one.one.one" {
		t.Errorf("cache entry for 1.1.1.1 is incorrect or missing")
	}

	fs = GetOrCreateFileState(logFilePath)
	if fs.LineNumber != 100 {
		t.Errorf("expected line number 100, got %d", fs.LineNumber)
	}
	// Verify that file metadata was written during save.
	if fs.FileSize == 0 {
		t.Error("expected file size to be non-zero after save")
	}
}

func TestFindMatchingPosition(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "find_pos_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logFilePath := filepath.Join(tmpDir, "test.log")
	logContent := "line 1\nline 2\nline 3 (the target)\nline 4\n"
	if err := os.WriteFile(logFilePath, []byte(logContent), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}
	info, _ := os.Stat(logFilePath)

	testCases := []struct {
		name          string
		storedState   *FileState
		expectedLine  int64
		expectedFound bool
	}{
		{
			name: "File unchanged",
			storedState: &FileState{
				LineNumber:   3,
				LineContent:  "line 3 (the target)",
				FileSize:     info.Size(),
				LastModified: info.ModTime().Unix(),
			},
			expectedLine:  3,
			expectedFound: true,
		},
		{
			name: "File metadata mismatch, but content found",
			storedState: &FileState{
				LineNumber:   3,
				LineContent:  "line 3 (the target)",
				FileSize:     1, // Mismatched size
				LastModified: 1, // Mismatched time
			},
			expectedLine:  3,
			expectedFound: true,
		},
		{
			name: "Content not found",
			storedState: &FileState{
				LineNumber:  5,
				LineContent: "this line does not exist",
			},
			expectedLine:  0,
			expectedFound: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			line, found := FindMatchingPosition(logFilePath, tc.storedState)
			if line != tc.expectedLine || found != tc.expectedFound {
				t.Errorf("Expected line %d, found %v. Got line %d, found %v", tc.expectedLine, tc.expectedFound, line, found)
			}
		})
	}
}
