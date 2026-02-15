package pipeline

import (
	"fmt"
	"log-enricher/internal/models"
	"log-enricher/internal/state"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func initParserState(t *testing.T) {
	t.Helper()
	require.NoError(t, state.Initialize(filepath.Join(t.TempDir(), "state.json")))
}

func TestJSONParser_Process(t *testing.T) {
	tests := []struct {
		name         string
		entries      []*models.LogEntry
		finalFields  map[string]any
		finalLogLine []byte
	}{
		{
			name: "parses valid object json",
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`{"level":"info","count":2,"ok":true}`),
					SourcePath: "app.log",
				},
			},
			finalFields: map[string]any{
				"level": "info",
				"count": float64(2),
				"ok":    true,
			},
			finalLogLine: []byte(`{"level":"info","count":2,"ok":true}`),
		},
		{
			name: "invalid json leaves empty fields",
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`{"broken":`),
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte(`{"broken":`),
		},
		{
			name: "skips parsing when fields already exist",
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{"existing": "value"},
					LogLine:    []byte(`{"ignored":"payload"}`),
					SourcePath: "app.log",
				},
			},
			finalFields: map[string]any{
				"existing": "value",
			},
			finalLogLine: []byte(`{"ignored":"payload"}`),
		},
		{
			name: "non-json prefix does not modify fields",
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`level=info`),
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte(`level=info`),
		},
		{
			name: "cache skips repeated failed first-byte/source combinations",
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`{"broken":`),
					SourcePath: "app.log",
				},
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`{"would":"parse_if_not_cached"}`),
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte(`{"would":"parse_if_not_cached"}`),
		},
		{
			name: "empty log line is kept",
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte{},
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initParserState(t)

			stage := NewJSONParser()
			for i, entry := range tt.entries {
				keep, err := stage.Process(entry)
				require.NoError(t, err, "entry %d", i)
				require.True(t, keep, "entry %d", i)
			}

			last := tt.entries[len(tt.entries)-1]
			require.Equal(t, tt.finalFields, last.Fields)
			require.Equal(t, tt.finalLogLine, last.LogLine)
		})
	}
}

func TestStructuredParser_Process(t *testing.T) {
	tests := []struct {
		name         string
		params       map[string]any
		entries      []*models.LogEntry
		finalFields  map[string]any
		finalLogLine []byte
		wantErr      string
	}{
		{
			name:   "default parser extracts key value pairs",
			params: map[string]any{},
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`level=info msg="hello world"`),
					SourcePath: "app.log",
				},
			},
			finalFields: map[string]any{
				"level": "info",
				"msg":   "hello world",
			},
			finalLogLine: []byte(`level=info msg="hello world"`),
		},
		{
			name: "named capture groups are extracted",
			params: map[string]any{
				"pattern": `level=(?P<level>\w+)\s+msg=(?P<msg>.*)`,
			},
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`level=warn msg=disk almost full`),
					SourcePath: "app.log",
				},
			},
			finalFields: map[string]any{
				"level": "warn",
				"msg":   "disk almost full",
			},
			finalLogLine: []byte(`level=warn msg=disk almost full`),
		},
		{
			name:   "unparsed trailing text clears partial fields",
			params: map[string]any{},
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`level=info trailing-text`),
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte(`level=info trailing-text`),
		},
		{
			name:   "skips parsing when fields already exist",
			params: map[string]any{},
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{"existing": "value"},
					LogLine:    []byte(`level=info`),
					SourcePath: "app.log",
				},
			},
			finalFields: map[string]any{
				"existing": "value",
			},
			finalLogLine: []byte(`level=info`),
		},
		{
			name:   "cache skips repeated failed first-byte/source combinations",
			params: map[string]any{},
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`invalid-format`),
					SourcePath: "app.log",
				},
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte(`id=42`),
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte(`id=42`),
		},
		{
			name:   "empty log line is kept",
			params: map[string]any{},
			entries: []*models.LogEntry{
				{
					Fields:     map[string]interface{}{},
					LogLine:    []byte{},
					SourcePath: "app.log",
				},
			},
			finalFields:  map[string]any{},
			finalLogLine: []byte{},
		},
		{
			name: "invalid regex shape is rejected",
			params: map[string]any{
				"pattern": `(\w+)`,
			},
			wantErr: "regex must have named capture groups, or exactly 2 unnamed capture groups for key-value pairs",
		},
		{
			name: "invalid regex syntax is rejected",
			params: map[string]any{
				"pattern": `(\w+`,
			},
			wantErr: "invalid structured log regex pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initParserState(t)

			stage, err := NewStructuredParser(tt.params)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			for i, entry := range tt.entries {
				keep, processErr := stage.Process(entry)
				require.NoError(t, processErr, "entry %d", i)
				require.True(t, keep, "entry %d", i)
			}

			last := tt.entries[len(tt.entries)-1]
			require.Equal(t, tt.finalFields, last.Fields, fmt.Sprintf("final fields for %q", tt.name))
			require.Equal(t, tt.finalLogLine, last.LogLine)
		})
	}
}
