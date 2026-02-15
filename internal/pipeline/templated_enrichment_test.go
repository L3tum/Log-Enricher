package pipeline

import (
	"log-enricher/internal/models"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTemplatedEnrichmentStage_NewTemplatedEnrichmentStage(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]interface{}
		wantErr bool
	}{
		{
			name: "valid config",
			params: map[string]interface{}{
				"template": "{{ .status }}",
				"field":    "new_status",
			},
			wantErr: false,
		},
		{
			name:    "missing template",
			params:  map[string]interface{}{"field": "new_status"},
			wantErr: true,
		},
		{
			name:    "missing field",
			params:  map[string]interface{}{"template": "{{ .status }}"},
			wantErr: true,
		},
		{
			name: "invalid template",
			params: map[string]interface{}{
				"template": "{{ .status | invalidFunc }}",
				"field":    "new_status",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTemplatedEnrichmentStage(tt.params)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTemplatedEnrichmentStage_Process(t *testing.T) {
	testCases := []struct {
		name           string
		template       string
		field          string
		inputFields    map[string]interface{}
		expectedOutput map[string]interface{}
		expectError    bool
	}{
		{
			name:     "simple field copy",
			template: "{{ .message }}",
			field:    "copied_message",
			inputFields: map[string]interface{}{
				"message": "hello world",
			},
			expectedOutput: map[string]interface{}{
				"message":        "hello world",
				"copied_message": "hello world",
			},
			expectError: false,
		},
		{
			name:     "numeric field conversion",
			template: "Status: {{ .status }}",
			field:    "status_string",
			inputFields: map[string]interface{}{
				"status": 200,
			},
			expectedOutput: map[string]interface{}{
				"status":        200,
				"status_string": "Status: 200",
			},
			expectError: false,
		},
		{
			name:     "missing field in template",
			template: "{{ .non_existent_field }}",
			field:    "result",
			inputFields: map[string]interface{}{
				"message": "test",
			},
			expectedOutput: map[string]interface{}{
				"message": "test",
				"result":  "<no value>", // Go template default for missing field
			},
			expectError: false,
		},
		{
			name:     "complex template with if-else",
			template: `{{ if eq .level "error" }}HIGH{{ else }}LOW{{ end }}`,
			field:    "priority",
			inputFields: map[string]interface{}{
				"level": "error",
			},
			expectedOutput: map[string]interface{}{
				"level":    "error",
				"priority": "HIGH",
			},
			expectError: false,
		},
		{
			name:     "complex template with if-else - other branch",
			template: `{{ if eq .level "error" }}HIGH{{ else }}LOW{{ end }}`,
			field:    "priority",
			inputFields: map[string]interface{}{
				"level": "info",
			},
			expectedOutput: map[string]interface{}{
				"level":    "info",
				"priority": "LOW",
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stage, err := NewTemplatedEnrichmentStage(map[string]interface{}{
				"template": tc.template,
				"field":    tc.field,
			})
			assert.NoError(t, err)

			entry := &models.LogEntry{
				Fields: tc.inputFields,
			}

			keep, err := stage.Process(entry)

			if tc.expectError {
				assert.Error(t, err)
				assert.True(t, keep) // Stage should still keep the entry even on template error
			} else {
				assert.NoError(t, err)
				assert.True(t, keep)
				assert.Equal(t, tc.expectedOutput, entry.Fields)
			}
		})
	}
}

func TestTemplatedEnrichmentStage_StatusGrouping(t *testing.T) {
	groupingTemplate := `
		{{- if and (ge .status 200) (lt .status 300) -}}2xx
		{{- else if and (ge .status 300) (lt .status 400) -}}3xx
		{{- else if eq .status 404 -}}404
		{{- else if and (ge .status 400) (le .status 403) -}}400-403
		{{- else if and (ge .status 400) (lt .status 500) -}}4xx
		{{- else if and (ge .status 500) (lt .status 600) -}}5xx
		{{- else -}}unknown
		{{- end -}}
	`
	targetField := "status_group"

	testCases := []struct {
		name          string
		statusCode    int
		expectedGroup string
	}{
		{"status 200", 200, "2xx"},
		{"status 204", 204, "2xx"},
		{"status 299", 299, "2xx"},
		{"status 301", 301, "3xx"},
		{"status 302", 302, "3xx"},
		{"status 399", 399, "3xx"},
		{"status 400", 400, "400-403"},
		{"status 401", 401, "400-403"},
		{"status 403", 403, "400-403"},
		{"status 404", 404, "404"}, // Specific 404
		{"status 405", 405, "4xx"},
		{"status 418", 418, "4xx"},
		{"status 499", 499, "4xx"},
		{"status 500", 500, "5xx"},
		{"status 503", 503, "5xx"},
		{"status 599", 599, "5xx"},
		{"status 100", 100, "unknown"}, // Informational
		{"status 600", 600, "unknown"}, // Beyond 5xx
		{"status -1", -1, "unknown"},   // Invalid
	}

	stage, err := NewTemplatedEnrichmentStage(map[string]interface{}{
		"template": groupingTemplate,
		"field":    targetField,
	})
	assert.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &models.LogEntry{
				Fields: map[string]interface{}{
					"status":  tc.statusCode,
					"message": "some log message",
				},
				Timestamp: time.Now(),
				LogLine:   []byte(""),
			}

			keep, processErr := stage.Process(entry)

			assert.NoError(t, processErr)
			assert.True(t, keep)
			assert.Contains(t, entry.Fields, targetField)
			assert.Equal(t, tc.expectedGroup, entry.Fields[targetField])
		})
	}
}
