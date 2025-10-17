package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"log-enricher/internal/models"
)

// CrowdsecConfig holds the configuration for the CrowdSec enrichment stage.
type CrowdsecConfig struct {
	LapiURL string `mapstructure:"crowdsec_lapi_url"`
	LapiKey string `mapstructure:"crowdsec_lapi_key"`
}

// CrowdsecStage communicates with a Crowdsec LAPI.
type CrowdsecStage struct {
	config *CrowdsecConfig
}

// NewCrowdsecStage creates a new stage for Crowdsec enrichment.
func NewCrowdsecStage(config *CrowdsecConfig) (*CrowdsecStage, error) {
	if config.LapiURL == "" || config.LapiKey == "" {
		return nil, fmt.Errorf("crowdsec LAPI URL or API key not configured")
	}
	return &CrowdsecStage{
		config: config,
	}, nil
}

// Name returns the name of the stage.
func (s *CrowdsecStage) Name() string {
	return "Crowdsec"
}

// Run performs the enrichment by querying the Crowdsec LAPI.
func (s *CrowdsecStage) Run(ip string, result *models.Result) (updated bool) {
	// Use a context to enforce a timeout for this specific request.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/v1/decisions?ip=%s", s.config.LapiURL, ip), nil)
	if err != nil {
		log.Printf("Crowdsec: failed to create request for %s: %v", ip, err)
		return false
	}
	req.Header.Add("X-Api-Key", s.config.LapiKey)

	resp, err := Get().Do(req)
	if err != nil {
		log.Printf("Crowdsec: failed to query for %s: %v", ip, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Crowdsec: non-200 response for %s: %s body: %s", ip, resp.Status, string(body))
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Crowdsec: failed to read response body for %s: %v", ip, err)
		return false
	}

	var decisions []models.CrowdsecDecision
	// Crowdsec can return "null" for no decisions
	if string(body) == "null" {
		result.Crowdsec = &models.CrowdsecInfo{IsBanned: false}
		return false
	}

	if err := json.Unmarshal(body, &decisions); err != nil {
		log.Printf("Crowdsec: failed to unmarshal decisions for %s: %v. Body: %s", ip, err, string(body))
		return false
	}

	if len(decisions) > 0 {
		result.Crowdsec = &models.CrowdsecInfo{
			IsBanned:  true,
			Decisions: decisions,
		}
		return true
	}

	result.Crowdsec = &models.CrowdsecInfo{IsBanned: false}
	return false
}
