package pipeline

import (
	"fmt"
	"log-enricher/internal/models"
	"log/slog"
	"strings"

	"github.com/mitchellh/mapstructure"
)

// FieldRewriteConfig holds the configuration for the GeoIP enrichment stage.
type FieldRewriteConfig struct {
	Rewrites      map[string]string `mapstructure:"rewrites"`
	KeepOldFields bool              `mapstructure:"keep_old_fields"`
}

type FieldRewriteStage struct {
	config   *FieldRewriteConfig
	rewrites map[string][]string
}

func (s *FieldRewriteStage) Name() string {
	return "field rewrite"
}

func (s *FieldRewriteStage) Process(entry *models.LogEntry) (keep bool, err error) {
	for fieldName, fieldPath := range s.rewrites {
		field, ok := getValueAtPath(entry.Fields, fieldPath)

		if !ok {
			continue
		}

		entry.Fields[fieldName] = field
	}

	if !s.config.KeepOldFields {
		for fieldName, _ := range entry.Fields {
			if _, ok := s.rewrites[fieldName]; !ok {
				delete(entry.Fields, fieldName)
			}
		}
	}

	return true, nil
}

// NewFieldRewriteStage creates a new stage for field rewriting
func NewFieldRewriteStage(params map[string]any) (Stage, error) {
	var stageConfig FieldRewriteConfig
	if err := mapstructure.Decode(params, &stageConfig); err != nil {
		return nil, fmt.Errorf("failed to decode field rewrite stage config: %w", err)
	}

	rewrites := make(map[string][]string)

	for key, value := range stageConfig.Rewrites {
		path := strings.Split(value, ".")

		rewrites[key] = path
	}

	stage := &FieldRewriteStage{
		config:   &stageConfig,
		rewrites: rewrites,
	}

	slog.Info("Field rewrite stage initialized")
	return stage, nil
}
