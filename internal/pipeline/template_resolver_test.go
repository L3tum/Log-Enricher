package pipeline

import (
	"log-enricher/internal/models"
	"log-enricher/internal/state"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMain sets up the slog logger for tests.
func TestMain(m *testing.M) {
	// Create a new logger that writes to stdout with debug level.
	// This will make slog.Debug messages visible during test execution.
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	// Run all tests
	exitCode := m.Run()

	// Exit with the test results
	os.Exit(exitCode)
}

func TestTemplateResolverStage_Process(t *testing.T) {
	// Initialize state since it's needed for certain functions
	err := state.Initialize("invalid_path")
	if err != nil {
		t.Errorf("Error initializing state %v", err)
	}

	tests := []struct {
		name          string
		config        map[string]interface{}
		inputEntry    *models.LogEntry
		expectedEntry *models.LogEntry
		expectedKeep  bool
		expectedErr   string
	}{
		{
			name: "Successful template resolution",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
				"placeholder":    `{{\.([0-9]+)}}`,
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":                    "Information",
					"MessageTemplate":          "{{.0}} {{.1}} after {{.2}} minute(s) and {{.3}} seconds", // Go template syntax
					"Properties.SourceContext": "Emby.Server.Implementations.ScheduledTasks.TaskManager",
					"Properties.ThreadId":      68,
					"Properties.0":             "Webhook Item Added Notifier",
					"Properties.1":             "Completed",
					"Properties.2":             0,
					"Properties.3":             0,
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":                    "Information",
					"MessageTemplate":          "{{.0}} {{.1}} after {{.2}} minute(s) and {{.3}} seconds",
					"Properties.SourceContext": "Emby.Server.Implementations.ScheduledTasks.TaskManager",
					"Properties.ThreadId":      68,
					"Properties.0":             "Webhook Item Added Notifier",
					"Properties.1":             "Completed",
					"Properties.2":             0,
					"Properties.3":             0,
					"RenderedMessage":          "Webhook Item Added Notifier Completed after 0 minute(s) and 0 seconds",
				},
			},
			expectedKeep: true,
			expectedErr:  "",
		},
		{
			name: "Successful template resolution with nested properties",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
				"placeholder":    `{{\.([0-9]+)}}`,
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": "{{.0}} {{.1}} after {{.2}} minute(s) and {{.3}} seconds", // Go template syntax
					"Properties": map[string]interface{}{
						"SourceContext": "Emby.Server.Implementations.ScheduledTasks.TaskManager",
						"ThreadId":      68,
						"0":             "Webhook Item Added Notifier",
						"1":             "Completed",
						"2":             0,
						"3":             0,
					},
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": "{{.0}} {{.1}} after {{.2}} minute(s) and {{.3}} seconds",
					"Properties": map[string]interface{}{
						"SourceContext": "Emby.Server.Implementations.ScheduledTasks.TaskManager",
						"ThreadId":      68,
						"0":             "Webhook Item Added Notifier",
						"1":             "Completed",
						"2":             0,
						"3":             0,
					},
					"RenderedMessage": "Webhook Item Added Notifier Completed after 0 minute(s) and 0 seconds",
				},
			},
			expectedKeep: true,
			expectedErr:  "",
		},
		{
			name: "Successful template resolution of non-Go template",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":                    "Information",
					"MessageTemplate":          "{0} {1} after {2} minute(s) and {3} seconds", // Non-Go template syntax
					"Properties.SourceContext": "Emby.Server.Implementations.ScheduledTasks.TaskManager",
					"Properties.ThreadId":      68,
					"Properties": map[string]interface{}{
						"0": "Webhook Item Added Notifier",
						"1": "Completed",
						"2": 0,
						"3": 0,
					},
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":                    "Information",
					"MessageTemplate":          "{0} {1} after {2} minute(s) and {3} seconds",
					"Properties.SourceContext": "Emby.Server.Implementations.ScheduledTasks.TaskManager",
					"Properties.ThreadId":      68,
					"Properties": map[string]interface{}{
						"0": "Webhook Item Added Notifier",
						"1": "Completed",
						"2": 0,
						"3": 0,
					},
					"RenderedMessage": "Webhook Item Added Notifier Completed after 0 minute(s) and 0 seconds",
				},
			},
			expectedKeep: true,
			expectedErr:  "",
		},
		{
			name: "Successful template resolution of non-Go template (named placeholders)",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":                "Information",
					"MessageTemplate":      "User {username} logged in from {ipAddress}", // Non-Go template syntax
					"Properties.username":  "testuser",
					"Properties.ipAddress": "192.168.1.1",
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":                "Information",
					"MessageTemplate":      "User {username} logged in from {ipAddress}",
					"Properties.username":  "testuser",
					"Properties.ipAddress": "192.168.1.1",
					"RenderedMessage":      "User testuser logged in from 192.168.1.1",
				},
			},
			expectedKeep: true,
			expectedErr:  "",
		},
		{
			name: "Template field missing, should skip and keep",
			config: map[string]interface{}{
				"template_field": "NonExistentTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":        "Information",
					"Properties.0": "Value",
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":        "Information",
					"Properties.0": "Value",
				},
			},
			expectedKeep: true,
			expectedErr:  "",
		},
		{
			name: "Template field not a string, should return error",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": 123, // Not a string
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": 123,
				},
			},
			expectedKeep: true, // Stage keeps the log but returns an error
			expectedErr:  "template field 'MessageTemplate' is not a string, got int",
		},
		{
			name: "Invalid Go template, should return error",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": "{{.0", // Invalid template syntax
					"Properties.0":    "Value",
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": "{{.0",
					"Properties.0":    "Value",
				},
			},
			expectedKeep: false, // Stage drops the log due to template parsing error
			expectedErr:  "failed to parse template from field 'MessageTemplate' (original: '{{.0', converted: '{{.0'): template: log_template:1: unclosed action",
		},
		{
			name: "Template with missing values, should render <no value> for missing",
			config: map[string]interface{}{
				"template_field": "MessageTemplate",
				"values_prefix":  "Properties.",
				"output_field":   "RenderedMessage",
				"placeholder":    `{{\.([0-9]+)}}`,
			},
			inputEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": "{{.0}} {{.1}} after {{.2}} minute(s) and {{.3}} seconds",
					"Properties.0":    "Webhook Item Added Notifier",
					// Properties.1, .2, .3 are missing
				},
			},
			expectedEntry: &models.LogEntry{
				Fields: map[string]interface{}{
					"Level":           "Information",
					"MessageTemplate": "{{.0}} {{.1}} after {{.2}} minute(s) and {{.3}} seconds",
					"Properties.0":    "Webhook Item Added Notifier",
					"RenderedMessage": "Webhook Item Added Notifier <no value> after <no value> minute(s) and <no value> seconds",
				},
			},
			expectedKeep: true,
			expectedErr:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, err := NewTemplateResolverStage(tt.config)
			assert.NoError(t, err)

			keep, processErr := stage.Process(tt.inputEntry)

			if tt.expectedErr != "" {
				assert.Error(t, processErr)
				assert.Contains(t, processErr.Error(), tt.expectedErr)
			} else {
				assert.NoError(t, processErr)
			}

			assert.Equal(t, tt.expectedKeep, keep)
			assert.Equal(t, tt.expectedEntry.Fields, tt.inputEntry.Fields)
		})
	}
}

func TestNewTemplateResolverStage_Validation(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]interface{}
		expectedErr string
	}{
		{
			name:        "Missing template_field",
			config:      map[string]interface{}{"values_prefix": "val.", "output_field": "out"},
			expectedErr: "template_field must be specified for template_resolver stage",
		},
		{
			name:        "Missing values_prefix",
			config:      map[string]interface{}{"template_field": "tmpl", "output_field": "out"},
			expectedErr: "values_prefix must be specified for template_resolver stage",
		},
		{
			name:        "Valid config, output_field defaults",
			config:      map[string]interface{}{"template_field": "tmpl", "values_prefix": "val."},
			expectedErr: "", // No error, output_field will default
		},
		{
			name:        "Valid config",
			config:      map[string]interface{}{"template_field": "tmpl", "values_prefix": "val.", "output_field": "out"},
			expectedErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTemplateResolverStage(tt.config)
			if tt.expectedErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
