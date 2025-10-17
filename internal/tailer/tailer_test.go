package tailer

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTailerTest creates a temporary file and a helper function to append to it.
// It returns the file path, the append helper, and a cleanup function.
func setupTailerTest(t *testing.T) (string, func(string), func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "tailer_test_*")
	require.NoError(t, err)

	logFilePath := filepath.Join(tmpDir, "test.log")
	file, err := os.Create(logFilePath)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	appender := func(content string) {
		// Use O_CREATE to ensure the file is created if it doesn't exist (e.g., after rotation).
		f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		require.NoError(t, err)
		_, err = f.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return logFilePath, appender, cleanup
}

func TestTailer(t *testing.T) {
	t.Run("Reads initial lines and appends", func(t *testing.T) {
		logFilePath, appender, cleanup := setupTailerTest(t)
		defer cleanup()

		appender("line 1\nline 2\n")

		// Start tailing from the beginning
		tailer := NewTailer(logFilePath, 0, io.SeekStart)
		tailer.Start()
		defer tailer.Stop()

		// Assert initial lines
		assert.Equal(t, "line 1", (<-tailer.Lines).Buffer.String())
		assert.Equal(t, "line 2", (<-tailer.Lines).Buffer.String())

		// Append a new line
		appender("line 3\n")
		assert.Equal(t, "line 3", (<-tailer.Lines).Buffer.String())
	})

	t.Run("Handles file truncation", func(t *testing.T) {
		logFilePath, appender, cleanup := setupTailerTest(t)
		defer cleanup()

		appender("line 1\nline 2\n")

		tailer := NewTailer(logFilePath, 0, io.SeekStart)
		tailer.Start()
		defer tailer.Stop()

		// Read initial lines
		<-tailer.Lines
		<-tailer.Lines

		// Truncate the file and write a new line
		require.NoError(t, os.Truncate(logFilePath, 0))
		appender("new line 1\n")

		// The tailer should detect truncation and read the new line
		select {
		case line := <-tailer.Lines:
			assert.Equal(t, "new line 1", line.Buffer.String())
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for line after truncation")
		}
	})

	t.Run("Handles file rotation", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping file rotation test on Windows due to file locking issues that prevent renaming an open file.")
		}

		logFilePath, appender, cleanup := setupTailerTest(t)
		defer cleanup()

		appender("old line 1\n")

		tailer := NewTailer(logFilePath, 0, io.SeekStart)
		tailer.Start()
		defer tailer.Stop()

		<-tailer.Lines // Consume the first line

		require.NoError(t, os.Rename(logFilePath, logFilePath+".old"))
		time.Sleep(10 * time.Millisecond) // Give tailer time to notice
		appender("new file line 1\n")

		select {
		case line := <-tailer.Lines:
			assert.Equal(t, "new file line 1", line.Buffer.String())
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for line after rotation")
		}
	})

	t.Run("Stop closes channels", func(t *testing.T) {
		logFilePath, _, cleanup := setupTailerTest(t)
		defer cleanup()

		tailer := NewTailer(logFilePath, 0, io.SeekEnd)
		tailer.Start()

		tailer.Stop()

		// Channels should be closed
		_, ok := <-tailer.Lines
		assert.False(t, ok, "Lines channel should be closed")
		_, ok = <-tailer.Errors
		assert.False(t, ok, "Errors channel should be closed")
	})
}
