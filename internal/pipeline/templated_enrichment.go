package pipeline

import (
	"bytes"
	"fmt"
	"log-enricher/internal/models"
	"log/slog"
	"reflect"
	"text/template"

	"github.com/mitchellh/mapstructure"
)

type TemplatedEnrichmentConfig struct {
	Template string `mapstructure:"template"`
	Field    string `mapstructure:"field"`
}

type TemplatedEnrichmentStage struct {
	config   *TemplatedEnrichmentConfig
	template *template.Template
}

// NewTemplatedEnrichmentStage creates a new TemplatedEnrichmentStage.
func NewTemplatedEnrichmentStage(params map[string]interface{}) (Stage, error) {
	var cfg TemplatedEnrichmentConfig
	if err := mapstructure.Decode(params, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode templated enrichment config: %w", err)
	}

	if cfg.Template == "" {
		return nil, fmt.Errorf("templated enrichment stage requires a 'template' string")
	}
	if cfg.Field == "" {
		return nil, fmt.Errorf("templated enrichment stage requires a 'field' to store the result")
	}

	// Parse the template. We name it "templated_enrichment" for error reporting.
	tmpl, err := template.New("templated_enrichment").Parse(cfg.Template)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template for templated enrichment stage: %w", err)
	}

	return &TemplatedEnrichmentStage{
		config:   &cfg,
		template: tmpl,
	}, nil
}

// Name returns the name of the stage.
func (s *TemplatedEnrichmentStage) Name() string {
	return "templated_enrichment"
}

// Process executes the configured template on the log entry's fields and stores the result.
func (s *TemplatedEnrichmentStage) Process(entry *models.LogEntry) (bool, error) {
	var buf bytes.Buffer
	// Execute the template using the log entry's fields as data.
	// The template can access fields like .status, .method, etc.
	if err := s.template.Execute(&buf, entry.Fields); err != nil {
		types := make(map[string]string, len(entry.Fields))

		for k, v := range entry.Fields {
			types[k] = reflect.TypeOf(v).Name()
		}

		slog.Error("failed to execute template", "fields", entry.Fields, "file", entry.SourcePath, "types", types, "error", err)
		return true, nil
	}

	// Store the template's output in the configured field.
	entry.Fields[s.config.Field] = buf.String()

	return true, nil // Always keep the log entry
}
