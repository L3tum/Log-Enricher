package enrichment

import "log-enricher/internal/models"

// Stage is an interface for an enrichment step.
type Stage interface {
	// Run performs the enrichment, modifying the result in place.
	// It should return true if it successfully added or changed data.
	Run(ip string, result *models.Result) (updated bool)
	// Name returns the name of the stage.
	Name() string
}
