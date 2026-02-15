package pipeline

import (
	"log-enricher/internal/models"
	"log-enricher/internal/state"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONParser_EmptyLogLine(t *testing.T) {
	require.NoError(t, state.Initialize(filepath.Join(t.TempDir(), "state.json")))

	stage := NewJSONParser()
	entry := &models.LogEntry{
		Fields:     map[string]interface{}{},
		LogLine:    []byte{},
		SourcePath: "test.log",
	}

	keep, err := stage.Process(entry)
	require.NoError(t, err)
	require.True(t, keep)
}

func TestStructuredParser_EmptyLogLine(t *testing.T) {
	require.NoError(t, state.Initialize(filepath.Join(t.TempDir(), "state.json")))

	stage, err := NewStructuredParser(map[string]any{})
	require.NoError(t, err)

	entry := &models.LogEntry{
		Fields:     map[string]interface{}{},
		LogLine:    []byte{},
		SourcePath: "test.log",
	}

	keep, processErr := stage.Process(entry)
	require.NoError(t, processErr)
	require.True(t, keep)
}
