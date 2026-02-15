package pipeline

import (
	"errors"
	"testing"

	"log-enricher/internal/cache"
	"log-enricher/internal/models"

	"github.com/oschwald/geoip2-golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeoIPEnrichmentStage_ReloadFailureKeepsExistingDB(t *testing.T) {
	oldDB := &geoip2.Reader{}
	stage := &GeoIpEnrichmentStage{
		config: &GeoIpConfig{
			DatabasePath: "/tmp/GeoLite2-City.mmdb",
		},
		db:                oldDB,
		clientIpToCountry: cache.NewPersistedCache[string]("geoip_reload_failure_test", 1, 10, false),
		openDB: func(string) (*geoip2.Reader, error) {
			return nil, errors.New("simulated reload error")
		},
	}

	stage.reloadDB()

	assert.Same(t, oldDB, stage.db)
}

func TestGeoIPEnrichmentStage_ProcessSkipsWhenDBUnavailable(t *testing.T) {
	stage := &GeoIpEnrichmentStage{
		config: &GeoIpConfig{
			ClientIpField:      "client_ip",
			ClientCountryField: "client_country",
		},
		clientIpToCountry: cache.NewPersistedCache[string]("geoip_process_nil_db_test", 1, 10, false),
	}

	entry := &models.LogEntry{
		Fields: map[string]any{
			"client_ip": "8.8.8.8",
		},
	}

	keep, err := stage.Process(entry)
	require.NoError(t, err)
	assert.True(t, keep)
	_, exists := entry.Fields["client_country"]
	assert.False(t, exists)
}
