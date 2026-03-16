package pipeline

import (
	"fmt"
	"log-enricher/internal/models"
	"log/slog"
	"strings"

	"github.com/goccy/go-json"
	"github.com/mitchellh/mapstructure"
)

// FieldRewriteConfig holds the configuration for the GeoIP enrichment stage.
type FieldRewriteConfig struct {
	Rewrites      string `mapstructure:"rewrites"`
	KeepOldFields bool   `mapstructure:"keep_old_fields"`
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
		for fieldName := range entry.Fields {
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
	// Simulates: FIELD_REWRITE_REWRITES='{"new_field":"old_field","another":"data.attributes.id"}'
	// In config.go, this gets read as a string from os.Getenv
	// Then mapstructure decodes it into just a string as well
	// The value needs to be unmarshaled as a map[string]string first
	// This should get better with a config file...
	var rewritesMap map[string]string
	err := json.Unmarshal([]byte(stageConfig.Rewrites), &rewritesMap)

	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal rewrite stage config: %w", err)
	}

	if len(rewritesMap) == 0 {
		return nil, fmt.Errorf("empty rewrite stage config")
	}

	rewrites := make(map[string][]string)

	for key, value := range rewritesMap {
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
