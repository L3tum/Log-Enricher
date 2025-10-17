package pipeline

import (
	"fmt"
	"log"
	"log-enricher/internal/bufferpool"
	"strings"

	"log-enricher/internal/cache"
	"log-enricher/internal/enrichment"
	"log-enricher/internal/models"

	"github.com/mitchellh/mapstructure"
)

// enricher is the interface for a single enrichment process, like geoip or hostname.
type enricher interface {
	// Run performs the enrichment, modifying the result in place.
	// It should return true if it successfully added or changed data.
	Run(ip string, result *models.Result) (updated bool)
	// Name returns the name of the stage.
	Name() string
}

// EnrichmentStage is a pipeline stage that applies one or more enrichments.
type EnrichmentStage struct {
	enrichers []enricher
}

// EnrichmentStageConfig defines which sub-enrichments are enabled for this stage.
type EnrichmentStageConfig struct {
	EnableHostname            bool                      `mapstructure:"enable_hostname"`
	EnableGeoIP               bool                      `mapstructure:"enable_geoip"`
	EnableCrowdsec            bool                      `mapstructure:"enable_crowdsec"`
	HostnameConfig            enrichment.HostnameConfig `mapstructure:"hostname,omitempty"`
	enrichment.GeoIpConfig    `mapstructure:",squash"`  // Use squash to flatten GeoIpConfig fields
	enrichment.CrowdsecConfig `mapstructure:",squash"`  // Use squash to flatten CrowdsecConfig fields
}

// NewEnrichmentStage creates a new enrichment stage.
func NewEnrichmentStage(params map[string]interface{}) (Stage, error) {
	var stageConfig EnrichmentStageConfig
	if err := mapstructure.WeakDecode(params, &stageConfig); err != nil {
		return nil, fmt.Errorf("failed to decode enrichment stage config: %w", err)
	}

	stage := &EnrichmentStage{}

	var initiatedEnrichers []string

	if stageConfig.EnableHostname {
		h := enrichment.NewHostnameStage(&stageConfig.HostnameConfig)
		stage.enrichers = append(stage.enrichers, h)
		initiatedEnrichers = append(initiatedEnrichers, h.Name())
	}
	if stageConfig.EnableGeoIP {
		g, err := enrichment.NewGeoIPStage(&stageConfig.GeoIpConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create geoip enrichment: %w", err)
		}
		if g != nil {
			stage.enrichers = append(stage.enrichers, g)
			initiatedEnrichers = append(initiatedEnrichers, g.Name())
		}
	}
	if stageConfig.EnableCrowdsec {
		c, err := enrichment.NewCrowdsecStage(&stageConfig.CrowdsecConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create crowdsec enrichment: %w", err)
		}
		if c != nil {
			stage.enrichers = append(stage.enrichers, c)
			initiatedEnrichers = append(initiatedEnrichers, c.Name())
		}
	}

	if len(initiatedEnrichers) > 0 {
		log.Printf("Enrichment stage enabled with: %s", strings.Join(initiatedEnrichers, ", "))
	}

	return stage, nil
}

func (s *EnrichmentStage) Name() string {
	return "enrichment"
}

func (s *EnrichmentStage) PerformEnrichment(clientIP string, result *models.Result) error {
	for _, e := range s.enrichers {
		e.Run(clientIP, result)
	}

	return nil
}

// Process extracts an IP from the log entry and applies all configured enrichments.
func (s *EnrichmentStage) Process(line []byte, entry *bufferpool.LogEntry) (bool, error) {
	if len(s.enrichers) == 0 || entry == nil {
		return true, nil // Nothing to do.
	}

	clientIP, ok := entry.Fields[ExtractedIPKey].(string)
	if !ok || clientIP == "" {
		return true, nil // No IP found to enrich.
	}

	var result models.Result
	var found bool
	if result, found = cache.Get(clientIP); !found {
		if s.PerformEnrichment(clientIP, &result) == nil {
			cache.Add(clientIP, result)
		}
	}

	if result.Hostname != "" {
		entry.Fields["client_hostname"] = result.Hostname
	}
	if result.Geo != nil && result.Geo.Country != "" {
		entry.Fields["client_country"] = result.Geo.Country
	}
	if result.Crowdsec != nil {
		entry.Fields["crowdsec_banned"] = result.Crowdsec.IsBanned
	}

	return true, nil
}
