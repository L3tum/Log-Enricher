package pipeline

import (
	"fmt"
	"log"

	"log-enricher/internal/config"
)

// Stage represents a single processing stage in the pipeline.
// The Process method takes the raw log line and the parsed JSON entry (if any).
// It returns true to keep the log, or false to drop it.
type Stage interface {
	Name() string
	Process(line []byte, entry map[string]interface{}) (keep bool, newEntry map[string]interface{}, err error)
}

type Manager interface {
	Process(line []byte, entry map[string]interface{}) (bool, map[string]interface{})
	EnrichmentStages() []EnrichmentStage
}

// Manager holds and executes the configured processing stages.
type manager struct {
	stages []Stage
}

// NewManager creates a new pipeline manager from the application config.
func NewManager(cfg *config.Config) (Manager, error) {
	var stages []Stage
	for i, stageCfg := range cfg.Stages {
		stage, err := newStage(stageCfg)
		if err != nil {
			return nil, fmt.Errorf("error creating stage %d (%s): %w", i, stageCfg.Type, err)
		}
		if stage != nil {
			stages = append(stages, stage)
			log.Printf("Enabled pipeline stage: %s", stage.Name())
		}
	}

	// Verify that the enrichment stage is preceded by the IP extraction stage.
	var enrichmentStageFound, extractionStageFound bool
	var enrichmentStageIndex, extractionStageIndex int
	for i, s := range stages {
		if _, ok := s.(*EnrichmentStage); ok {
			enrichmentStageFound = true
			enrichmentStageIndex = i
		}
		if _, ok := s.(*ClientIpExtractionStage); ok {
			extractionStageFound = true
			extractionStageIndex = i
		}
	}

	if enrichmentStageFound && (!extractionStageFound || extractionStageIndex > enrichmentStageIndex) {
		return nil, fmt.Errorf("enrichment stage requires a 'client_ip_extraction' stage to be configured before it in the pipeline")
	}

	return &manager{stages: stages}, nil
}

// Process runs a log entry through the entire pipeline.
func (m *manager) Process(line []byte, entry map[string]interface{}) (bool, map[string]interface{}) {
	keep := true
	var err error

	for _, stage := range m.stages {
		keep, entry, err = stage.Process(line, entry)
		if err != nil {
			log.Printf("Error during stage '%s': %v. Dropping log entry.", stage.Name(), err)
			return false, nil
		}
		if !keep {
			// Stage decided to drop the log, so we stop processing.
			log.Printf("Log dropped by stage: %s", stage.Name())
			return false, nil
		}
	}
	return true, entry
}

// EnrichmentStages returns the configured enrichment stages.
func (m *manager) EnrichmentStages() []EnrichmentStage {
	var stages []EnrichmentStage

	for _, s := range m.stages {
		if stage, ok := s.(*EnrichmentStage); ok {
			stages = append(stages, *stage)
		}
	}
	return stages
}

// newStage is a factory function to create stages from config.
func newStage(stageCfg config.StageConfig) (Stage, error) {
	switch stageCfg.Type {
	case "filter":
		return NewFilterStage(stageCfg.Params)
	case "enrichment":
		return NewEnrichmentStage(stageCfg.Params)
	case "client_ip_extraction":
		return NewClientIpExtractionStage(stageCfg.Params)
	default:
		return nil, fmt.Errorf("unknown stage type: %s", stageCfg.Type)
	}
}
