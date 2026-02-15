package state

import (
	"log-enricher/internal/models"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestState creates a temporary directory and provides a cleanup function
// that removes the directory and resets the global state for test isolation.
func setupTestState(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "state_test_*")
	require.NoError(t, err, "Failed to create temp dir")

	cleanup := func() {
		os.RemoveAll(tmpDir)
		// Reset the global state after each test to ensure isolation.
		globalState = nil
	}

	return tmpDir, cleanup
}

// This test suite covers the entire lifecycle of state management.
func TestStateLifecycle(t *testing.T) {
	t.Run("Save and Load successfully", func(t *testing.T) {
		tmpDir, cleanup := setupTestState(t)
		defer cleanup()

		stateFilePath := filepath.Join(tmpDir, "state.json")
		logFilePath := filepath.Join(tmpDir, "app.log")

		// Create a dummy log file so os.Stat works during save.
		require.NoError(t, os.WriteFile(logFilePath, []byte("dummy line"), 0644))

		// 1. Initialize and populate a state.
		require.NoError(t, Initialize(stateFilePath))

		fs := GetOrCreateFileState(logFilePath)
		fs.LineNumber = 100
		SetCacheEntry("1.1.1.1", models.Result{Hostname: "one.one.one.one"})

		// 2. Save the state.
		require.NoError(t, Save(stateFilePath))

		// 3. Re-initialize, which will load the saved state.
		require.NoError(t, Initialize(stateFilePath))

		// 4. Verify the loaded state.
		assert.Equal(t, 1, GetCacheSize(), "expected cache size of 1")
		res, ok := GetCacheEntry("1.1.1.1")
		assert.True(t, ok, "cache entry for 1.1.1.1 should exist")
		assert.Equal(t, "one.one.one.one", res.Hostname)

		loadedFs := GetOrCreateFileState(logFilePath)
		assert.Equal(t, int64(100), loadedFs.LineNumber)
		assert.NotZero(t, loadedFs.FileSize, "expected file size to be non-zero after save")
		assert.NotZero(t, loadedFs.LastModified, "expected last modified to be non-zero after save")
	})

	t.Run("Load from non-existent file", func(t *testing.T) {
		tmpDir, cleanup := setupTestState(t)
		defer cleanup()

		stateFilePath := filepath.Join(tmpDir, "nonexistent.json")
		err := Initialize(stateFilePath)
		require.NoError(t, err)
		assert.NotNil(t, globalState)
		assert.Empty(t, globalState.Files)
		assert.Empty(t, globalState.Cache)
	})

	t.Run("Load from corrupted file", func(t *testing.T) {
		tmpDir, cleanup := setupTestState(t)
		defer cleanup()

		stateFilePath := filepath.Join(tmpDir, "corrupted.json")
		require.NoError(t, os.WriteFile(stateFilePath, []byte("{not valid json}"), 0644))

		err := Initialize(stateFilePath)
		assert.Error(t, err)
	})

	t.Run("Initialize with empty path fails", func(t *testing.T) {
		_, cleanup := setupTestState(t)
		defer cleanup()

		err := Initialize("")
		assert.Error(t, err)
	})
}

// This new test suite focuses on file-specific state operations.
func TestFileStateOperations(t *testing.T) {
	t.Run("GetOrCreateFileState", func(t *testing.T) {
		tmpDir, cleanup := setupTestState(t)
		defer cleanup()
		require.NoError(t, Initialize(filepath.Join(tmpDir, "state.json")))

		// First call should create a new state.
		fs1 := GetOrCreateFileState("path/to/file.log")
		require.NotNil(t, fs1)
		assert.Equal(t, "path/to/file.log", fs1.Path)
		assert.Equal(t, 1, len(globalState.Files))

		// Second call should return the existing state.
		fs2 := GetOrCreateFileState("path/to/file.log")
		assert.Same(t, fs1, fs2, "should return the same instance")
		assert.Equal(t, 1, len(globalState.Files))
	})

	t.Run("UpdateAllFileMetadata", func(t *testing.T) {
		tmpDir, cleanup := setupTestState(t)
		defer cleanup()
		require.NoError(t, Initialize(filepath.Join(tmpDir, "state.json")))

		existingLog := filepath.Join(tmpDir, "exists.log")
		deletedLog := filepath.Join(tmpDir, "deleted.log")

		require.NoError(t, os.WriteFile(existingLog, []byte("content"), 0644))
		require.NoError(t, os.WriteFile(deletedLog, []byte("content"), 0644))

		_ = GetOrCreateFileState(existingLog)
		_ = GetOrCreateFileState(deletedLog)
		require.Equal(t, 2, len(globalState.Files))

		// Now delete one of the files.
		require.NoError(t, os.Remove(deletedLog))

		UpdateAllFileMetadata()

		assert.Equal(t, 1, len(globalState.Files), "state for deleted file should be removed")
		_, exists := globalState.Files[deletedLog]
		assert.False(t, exists)

		fs := globalState.Files[existingLog]
		require.NotNil(t, fs)
		assert.NotZero(t, fs.FileSize)
		assert.NotZero(t, fs.LastModified)
	})
}

// This test is refactored for isolation and expanded with more cases.
func TestFindMatchingPosition(t *testing.T) {
	tmpDir, cleanup := setupTestState(t)
	defer cleanup()

	logFilePath := filepath.Join(tmpDir, "test.log")
	logContent := "line 1\nline 2\n"
	require.NoError(t, os.WriteFile(logFilePath, []byte(logContent), 0644))
	info, _ := os.Stat(logFilePath)
	inode, _ := getInode(info)

	testCases := []struct {
		name          string
		storedState   *FileState
		fileModifier  func() // Optional function to modify the file for the test case
		expectedLine  int64
		expectedFound bool
	}{
		{
			name: "File unchanged, should match",
			storedState: &FileState{
				LineNumber:   2,
				FileSize:     info.Size(),
				LastModified: info.ModTime().Unix(),
				Inode:        inode,
			},
			expectedLine:  2,
			expectedFound: true,
		},
		{
			name: "Same inode and size but stale modtime, should not match",
			storedState: &FileState{
				LineNumber:   2,
				FileSize:     info.Size(),
				LastModified: info.ModTime().Unix() - 60,
				Inode:        inode,
			},
			expectedLine:  0,
			expectedFound: false,
		},
		{
			name: "Inode mismatch (rotation), should not match",
			storedState: &FileState{
				LineNumber:   2,
				FileSize:     info.Size(),
				LastModified: info.ModTime().Unix(),
				Inode:        inode, // Storing the original inode
			},
			fileModifier: func() {
				// Simulate rotation: delete and recreate the file.
				require.NoError(t, os.Remove(logFilePath))
				// Sleep for a duration longer than the filesystem's timestamp resolution
				// to guarantee the new file has a different modification time.
				time.Sleep(1000 * time.Millisecond)
				require.NoError(t, os.WriteFile(logFilePath, []byte(logContent), 0644))
			},
			expectedLine:  0,
			expectedFound: false,
		},
		{
			name: "File truncated (same inode), should not match",
			storedState: &FileState{
				LineNumber:   2,
				FileSize:     info.Size(),
				LastModified: info.ModTime().Unix(),
				Inode:        inode,
			},
			fileModifier: func() {
				// Truncate the file to a smaller size.
				require.NoError(t, os.Truncate(logFilePath, 5))
			},
			expectedLine:  0,
			expectedFound: false,
		},
		{
			name: "No stored metadata, should not match",
			storedState: &FileState{
				LineNumber: 2, // No FileSize or LastModified
			},
			expectedLine:  0,
			expectedFound: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset file content for each run
			require.NoError(t, os.WriteFile(logFilePath, []byte(logContent), 0644))
			if tc.fileModifier != nil {
				tc.fileModifier()
			}
			// We need to re-initialize the state for logging to work inside the function
			require.NoError(t, Initialize(filepath.Join(tmpDir, "state.json")))

			line, found := FindMatchingPosition(logFilePath, tc.storedState)
			assert.Equal(t, tc.expectedLine, line, "line number mismatch")
			assert.Equal(t, tc.expectedFound, found, "found status mismatch")
		})
	}

	t.Run("File does not exist", func(t *testing.T) {
		require.NoError(t, Initialize(filepath.Join(tmpDir, "state.json")))
		state := &FileState{LineNumber: 1, FileSize: 10, LastModified: 100}
		line, found := FindMatchingPosition("non/existent/file.log", state)
		assert.Equal(t, int64(0), line)
		assert.False(t, found)
	})
}
