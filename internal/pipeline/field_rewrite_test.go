package pipeline

import (
	"testing"

	"log-enricher/internal/models"

	"github.com/stretchr/testify/assert"
)

func TestNewFieldRewriteStage(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]interface{}
		wantErr bool
	}{
		{
			name:    "Valid empty config",
			params:  map[string]interface{}{},
			wantErr: true, // Empty rewrites should error
		},
		{
			name: "Valid simple rewrites",
			params: map[string]interface{}{
				"rewrites": `{"new_field":"old_field"}`,
			},
			wantErr: false,
		},
		{
			name: "Valid nested path rewrites",
			params: map[string]interface{}{
				"rewrites": `{"new_field":"data.attributes.id"}`,
			},
			wantErr: false,
		},
		{
			name: "Valid keep_old_fields",
			params: map[string]interface{}{
				"rewrites":        `{"new_field":"old_field"}`,
				"keep_old_fields": true,
			},
			wantErr: false,
		},
		{
			name: "Invalid rewrites type",
			params: map[string]interface{}{
				"rewrites": "not_a_map",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFieldRewriteStage(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewFieldRewriteStage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFieldRewriteStage_Process_SimpleRewrite(t *testing.T) {
	t.Run("Rewrite single field", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"renamed_field":"original_field"}`,
			"keep_old_fields": false,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"original_field": "some_value",
				"other_field":    "other_value",
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that the field was renamed
		assert.Equal(t, "some_value", entry.Fields["renamed_field"])
		// Check that old field was removed (keep_old_fields=false)
		_, exists := entry.Fields["original_field"]
		assert.False(t, exists)
		// Check that other field was removed (keep_old_fields=false)
		_, exists = entry.Fields["other_field"]
		assert.False(t, exists)
	})

	t.Run("Rewrite with keep_old_fields", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"renamed_field":"original_field"}`,
			"keep_old_fields": true,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"original_field": "some_value",
				"other_field":    "other_value",
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that both fields exist
		assert.Equal(t, "some_value", entry.Fields["renamed_field"])
		assert.Equal(t, "some_value", entry.Fields["original_field"])
		assert.Equal(t, "other_value", entry.Fields["other_field"])
	})
}

func TestFieldRewriteStage_Process_NestedPath(t *testing.T) {
	t.Run("Rewrite nested field", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"processed_id":"data.attributes.id"}`,
			"keep_old_fields": false,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"data": map[string]interface{}{
					"attributes": map[string]interface{}{
						"id":       "12345",
						"category": "logs",
					},
				},
				"metadata": map[string]interface{}{
					"version": "1.0",
				},
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that nested value was extracted
		assert.Equal(t, "12345", entry.Fields["processed_id"])
		// Check that old nested field was removed
		_, exists := entry.Fields["data"]
		assert.False(t, exists)
		// Check that other field was removed
		_, exists = entry.Fields["metadata"]
		assert.False(t, exists)
	})
}

func TestFieldRewriteStage_Process_MultipleRewrites(t *testing.T) {
	t.Run("Multiple field rewrites", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"new_id":"id", "new_message": "message"}`,
			"keep_old_fields": false,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"id":        "123",
				"message":   "test message",
				"level":     "info",
				"source":    "app",
				"timestamp": "2024-01-01T00:00:00Z",
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that rewrites were applied
		assert.Equal(t, "123", entry.Fields["new_id"])
		assert.Equal(t, "test message", entry.Fields["new_message"])
		// Check that other fields were removed
		_, exists := entry.Fields["id"]
		assert.False(t, exists)
		_, exists = entry.Fields["message"]
		assert.False(t, exists)
		_, exists = entry.Fields["level"]
		assert.False(t, exists)
		_, exists = entry.Fields["source"]
		assert.False(t, exists)
		_, exists = entry.Fields["timestamp"]
		assert.False(t, exists)
	})
}

func TestFieldRewriteStage_Process_FieldNotFound(t *testing.T) {
	t.Run("Source field not found", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"new_field":"nonexistent_field"}`,
			"keep_old_fields": false,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"other_field": "some_value",
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that the field was not created
		_, exists := entry.Fields["new_field"]
		assert.False(t, exists)
		// Check that other field was removed (keep_old_fields=false)
		_, exists = entry.Fields["other_field"]
		assert.False(t, exists)
	})
}

func TestFieldRewriteStage_Process_NestedPathNotFound(t *testing.T) {
	t.Run("Nested path with missing intermediate", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"new_field":"data.attributes.id"}`,
			"keep_old_fields": false,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"data": map[string]interface{}{
					"other": "value",
				},
				"metadata": "other",
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that the field was not created
		_, exists := entry.Fields["new_field"]
		assert.False(t, exists)
		// Check that other field was removed
		_, exists = entry.Fields["metadata"]
		assert.False(t, exists)
	})

	t.Run("Nested path with wrong intermediate type", func(t *testing.T) {
		params := map[string]interface{}{
			"rewrites":        `{"new_field":"data.attributes.id"}`,
			"keep_old_fields": false,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		entry := &models.LogEntry{
			Fields: map[string]interface{}{
				"data":  "not_a_map", // This is a string, not a map
				"other": "value",
			},
		}

		keep, err := stage.Process(entry)
		assert.True(t, keep)
		assert.NoError(t, err)

		// Check that the field was not created
		_, exists := entry.Fields["new_field"]
		assert.False(t, exists)
		// Check that other field was removed
		_, exists = entry.Fields["other"]
		assert.False(t, exists)
	})
}

// TestEnvironmentVariableParsing tests how environment variables are parsed into map[string][]string
func TestEnvironmentVariableParsing(t *testing.T) {
	t.Run("JSON format env var", func(t *testing.T) {
		// Simulates: FIELD_REWRITE_REWRITES='{"new_field":"old_field","another":"data.attributes.id"}'
		jsonEnv := `{"new_field":"old_field","another":"data.attributes.id"}`

		// Then this gets passed to NewFieldRewriteStage
		params := map[string]interface{}{
			"rewrites": jsonEnv,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)

		// Verify the rewrites are properly converted to map[string][]string
		// This would need to access the private field via reflection or add a getter
		// For now, just verify stage creation works
		assert.NotNil(t, stage)
	})

	t.Run("JSON nested path with special characters", func(t *testing.T) {
		// JSON format handles special characters in keys
		jsonEnv := `{"field_with_underscore":"source.field","field-with-dash":"another.path"}`

		params := map[string]interface{}{
			"rewrites": jsonEnv,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.NoError(t, err)
		assert.NotNil(t, stage)
	})

	t.Run("Empty rewrites JSON", func(t *testing.T) {
		jsonEnv := `{}`

		params := map[string]interface{}{
			"rewrites": jsonEnv,
		}

		stage, err := NewFieldRewriteStage(params)
		assert.Error(t, err)
		assert.Nil(t, stage)
	})
}
