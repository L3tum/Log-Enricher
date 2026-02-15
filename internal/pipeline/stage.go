package pipeline

import (
	"context"
	"fmt"
	"log-enricher/internal/models"
	"log/slog"
	"regexp"

	"log-enricher/internal/config"
)

// Stage represents a single processing stage in the pipeline.
// The Process method takes the raw log line and the parsed JSON entry (if any).
// It returns true to keep the log, or false to drop it.
type Stage interface {
	Name() string
	Process(entry *models.LogEntry) (keep bool, err error)
}

type ProcessPipeline interface {
	Process(entry *models.LogEntry) bool
}

type Manager interface {
	GetProcessPipeline(filePath string) ProcessPipeline
}

type appliedToStage struct {
	stage     Stage
	appliesTo *regexp.Regexp
}

// Manager holds and executes the configured processing stages.
type manager struct {
	stages []appliedToStage
}

// NewManager creates a new pipeline manager from the application config.
func NewManager(cfg *config.Config, ctx context.Context) (Manager, error) {
	var stages []appliedToStage

	for i, stageCfg := range cfg.Stages {
		stage, appliesTo, err := newStage(stageCfg, ctx)
		if err != nil {
			return nil, fmt.Errorf("error creating stage %d (%s): %w", i, stageCfg.Type, err)
		}
		if stage != nil {
			stages = append(stages, appliedToStage{appliesTo: appliesTo, stage: stage})
			slog.Debug("Enabled pipeline stage", "stage", stage.Name())
		}
	}

	return &manager{stages: stages}, nil
}

func (m *manager) GetProcessPipeline(filePath string) ProcessPipeline {
	var stages []Stage
	for _, stage := range m.stages {
		if stage.appliesTo != nil {
			if stage.appliesTo.MatchString(filePath) {
				stages = append(stages, stage.stage)
			} else {
				slog.Debug("Ignoring stage due to 'applies_to' regex", "stage", stage.stage.Name(), "regex", stage.appliesTo, "path", filePath)
			}
		} else {
			stages = append(stages, stage.stage)
		}
	}

	return &processPipeline{stages: stages}
}

type processPipeline struct {
	stages []Stage
}

// Process runs a log entry through the entire pipeline.
func (m *processPipeline) Process(entry *models.LogEntry) bool {
	keep := true
	var err error

	for _, stage := range m.stages {
		keep, err = stage.Process(entry)
		if err != nil {
			slog.Error("Error during stage", "stage", stage.Name(), "error", err)
		}
		if !keep {
			// Stage decided to drop the log, so we stop processing.
			return false
		}
	}
	return true
}

// newStage is a factory function to create stages from config.
func newStage(stageCfg config.StageConfig, ctx context.Context) (Stage, *regexp.Regexp, error) {
	var stage Stage
	var err error

	switch stageCfg.Type {
	case "filter":
		stage, err = NewFilterStage(stageCfg.Params)
	case "client_ip_extraction":
		stage, err = NewClientIpExtractionStage(stageCfg.Params)
	case "timestamp_extraction": // Added for completeness, though usually added automatically
		stage, err = NewTimestampExtractionStage(stageCfg.Params)
	case "hostname_enrichment":
		stage, err = NewHostnameEnrichmentStage(ctx, stageCfg.Params)
	case "geoip_enrichment":
		stage, err = NewGeoIPStage(ctx, stageCfg.Params)
	case "template_resolver":
		stage, err = NewTemplateResolverStage(stageCfg.Params)
	case "templated_enrichment":
		stage, err = NewTemplatedEnrichmentStage(stageCfg.Params)
	case "json_parser":
		stage = NewJSONParser()
	case "structured_parser":
		stage, err = NewStructuredParser(stageCfg.Params)
	default:
		return nil, nil, fmt.Errorf("unknown stage type: %s", stageCfg.Type)
	}

	if err != nil {
		return nil, nil, err
	}

	// If an AppliesTo regex is configured, wrap the stage with the filter.
	if stageCfg.AppliesTo != "" {
		regex, compileErr := regexp.Compile(stageCfg.AppliesTo)
		if compileErr != nil || regex == nil {
			return nil, nil, fmt.Errorf("invalid 'applies_to' regex for stage %s: %w", stageCfg.Type, compileErr)
		}

		slog.Debug("Applying 'AppliesTo' filter to stage", "stage", stage.Name(), "regex", stageCfg.AppliesTo)
		return stage, regex, nil
	}

	return stage, nil, nil
}
