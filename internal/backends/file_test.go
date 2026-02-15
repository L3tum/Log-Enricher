package backends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"log-enricher/internal/models"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileBackendSend_WritesJSONWhenFieldsExist(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "nested", "app.log")
	backend := NewFileBackend(".enriched")
	defer backend.Shutdown()

	entry := &models.LogEntry{
		SourcePath: sourcePath,
		Fields: map[string]interface{}{
			"level": "info",
			"msg":   "hello",
		},
	}

	require.NoError(t, backend.Send(entry))

	content, err := os.ReadFile(sourcePath + ".enriched")
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(content))), &decoded))
	assert.Equal(t, "info", decoded["level"])
	assert.Equal(t, "hello", decoded["msg"])
	assert.True(t, strings.HasSuffix(string(content), "\n"))
}

func TestFileBackendSend_WritesRawLineWhenNoFields(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "raw.log")
	backend := NewFileBackend(".enriched")
	defer backend.Shutdown()

	entry := &models.LogEntry{
		SourcePath: sourcePath,
		LogLine:    []byte("plain-text-line"),
		Fields:     map[string]interface{}{},
	}

	require.NoError(t, backend.Send(entry))

	content, err := os.ReadFile(sourcePath + ".enriched")
	require.NoError(t, err)
	assert.Equal(t, "plain-text-line\n", string(content))
}

func TestFileBackendSend_WritesSingleNewlineForEmptyInput(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "empty.log")
	backend := NewFileBackend(".enriched")
	defer backend.Shutdown()

	entry := &models.LogEntry{
		SourcePath: sourcePath,
		Fields:     map[string]interface{}{},
	}

	require.NoError(t, backend.Send(entry))

	content, err := os.ReadFile(sourcePath + ".enriched")
	require.NoError(t, err)
	assert.Equal(t, "\n", string(content))
}

func TestFileBackendCloseWriter_AllowsReopenAndAppend(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "reopen.log")
	backend := NewFileBackend(".enriched")
	defer backend.Shutdown()

	require.NoError(t, backend.Send(&models.LogEntry{
		SourcePath: sourcePath,
		LogLine:    []byte("line-one"),
		Fields:     map[string]interface{}{},
	}))

	backend.CloseWriter(sourcePath)

	require.NoError(t, backend.Send(&models.LogEntry{
		SourcePath: sourcePath,
		LogLine:    []byte("line-two"),
		Fields:     map[string]interface{}{},
	}))

	content, err := os.ReadFile(sourcePath + ".enriched")
	require.NoError(t, err)
	assert.Equal(t, "line-one\nline-two\n", string(content))
}
