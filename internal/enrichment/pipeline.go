package enrichment

import (
	"log"
	"strings"

	"log-enricher/internal/cache"
	"log-enricher/internal/models"
)

// Pipeline manages a series of enrichment stages.
type Pipeline struct {
	stages []Stage
}

// Enricher defines the interface for the enrichment service.
type Enricher interface {
	Enrich(ip string) models.Result
	PerformEnrichment(ip string) models.Result
}

// NewPipeline creates a new enrichment pipeline. It filters out any nil stages.
func NewPipeline(stages ...Stage) *Pipeline {
	p := &Pipeline{}
	var stageNames []string
	for _, s := range stages {
		if s != nil {
			p.stages = append(p.stages, s)
			stageNames = append(stageNames, s.Name())
		}
	}
	log.Printf("Enrichment pipeline initialized with stages: %s", strings.Join(stageNames, ", "))
	return p
}

// Enrich gets enrichment results for an IP, using the cache if possible.
func (p *Pipeline) Enrich(ip string) models.Result {
	// Check cache first
	if val, ok := cache.Get(ip); ok {
		return val
	}

	// Perform the actual lookup
	res := p.PerformEnrichment(ip)

	// Add to cache regardless of whether it was found. This implements negative caching.
	cache.Add(ip, res)
	return res
}

// PerformEnrichment runs all enrichment stages for an IP, bypassing the cache.
func (p *Pipeline) PerformEnrichment(ip string) models.Result {
	result := models.Result{}
	var anyFound bool

	for _, stage := range p.stages {
		if updated := stage.Run(ip, &result); updated {
			anyFound = true
		}
	}
	result.Found = anyFound || result.Hostname != "" || result.MAC != "" || (result.Geo != nil && result.Geo.Country != "")
	return result
}
