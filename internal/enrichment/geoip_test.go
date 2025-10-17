package enrichment

import (
	"net"
	"os"
	"testing"
	"time"

	"log-enricher/internal/models"

	// Step 1: Corrected the typo in the import path.
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestDB creates a temporary GeoIP database for testing.
func createTestDB(t *testing.T, ip string, city string, country string) string {
	t.Helper()

	tmpfile, err := os.CreateTemp("", "test-geoip-*.mmdb")
	require.NoError(t, err)

	writer, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "GeoIP2-City"})
	require.NoError(t, err)

	ipNet := net.ParseIP(ip)
	_, network, err := net.ParseCIDR(ipNet.String() + "/32")
	require.NoError(t, err)

	err = writer.Insert(network, mmdbtype.Map{
		"city": mmdbtype.Map{
			"names": mmdbtype.Map{
				"en": mmdbtype.String(city),
			},
		},
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String(country),
		},
	})
	require.NoError(t, err)

	_, err = writer.WriteTo(tmpfile)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	return tmpfile.Name()
}

func TestNewGeoIPStage(t *testing.T) {
	t.Run("disabled when path is empty", func(t *testing.T) {
		stage, err := NewGeoIPStage(&GeoIpConfig{DatabasePath: ""})
		assert.Nil(t, stage)
		assert.NoError(t, err)
	})

	t.Run("error on invalid db path", func(t *testing.T) {
		stage, err := NewGeoIPStage(&GeoIpConfig{DatabasePath: "nonexistent/path/to/db.mmdb"})
		assert.Nil(t, stage)
		assert.Error(t, err)
	})

	t.Run("successful creation", func(t *testing.T) {
		dbPath := createTestDB(t, "8.8.8.8", "Mountain View", "US")
		config := &GeoIpConfig{DatabasePath: dbPath}

		stage, err := NewGeoIPStage(config)
		defer os.Remove(dbPath)

		require.NotNil(t, stage)
		require.NoError(t, err)

		assert.Equal(t, "GeoIP", stage.Name())
	})
}

func TestGeoIPStage_Run(t *testing.T) {
	dbPath := createTestDB(t, "8.8.8.8", "Mountain View", "US")
	config := &GeoIpConfig{DatabasePath: dbPath}

	stage, err := NewGeoIPStage(config)
	require.NotNil(t, stage)
	require.NoError(t, err)
	defer os.Remove(dbPath)

	t.Run("enrich public IP", func(t *testing.T) {
		result := &models.Result{}
		updated := stage.Run("8.8.8.8", result)

		assert.True(t, updated)
		require.NotNil(t, result.Geo)
		assert.Equal(t, "US", result.Geo.Country)
		assert.Equal(t, "Mountain View", result.Geo.City)
	})

	t.Run("skip private IP", func(t *testing.T) {
		result := &models.Result{}
		updated := stage.Run("192.168.1.1", result)
		assert.False(t, updated)
		assert.Nil(t, result.Geo)
	})

	t.Run("handle invalid IP", func(t *testing.T) {
		result := &models.Result{}
		updated := stage.Run("not-an-ip", result)
		assert.False(t, updated)
		assert.Nil(t, result.Geo)
	})

	t.Run("IP not in database", func(t *testing.T) {
		result := &models.Result{}
		updated := stage.Run("1.1.1.1", result)
		assert.False(t, updated)
		assert.Nil(t, result.Geo)
	})

	t.Run("database is nil", func(t *testing.T) {
		// Temporarily set db to nil
		db := stage.db
		stage.db = nil
		defer func() { stage.db = db }()

		result := &models.Result{}
		updated := stage.Run("8.8.8.8", result)
		assert.False(t, updated)
	})
}

func TestGeoIPStage_Reload(t *testing.T) {
	dbPath := createTestDB(t, "8.8.8.8", "Mountain View", "US")
	config := &GeoIpConfig{DatabasePath: dbPath}

	stage, err := NewGeoIPStage(config)
	require.NotNil(t, stage)
	require.NoError(t, err)
	defer os.Remove(dbPath)

	// First, check the initial data.
	result1 := &models.Result{}
	stage.Run("8.8.8.8", result1)
	require.NotNil(t, result1.Geo)
	assert.Equal(t, "Mountain View", result1.Geo.City)

	// Create a new DB file with updated data, then atomically rename it over the old one.
	newDbPath := createTestDB(t, "8.8.8.8", "Palo Alto", "US")
	err = os.Rename(newDbPath, dbPath)
	require.NoError(t, err)

	// Allow some time for the watcher to detect the change and reload.
	time.Sleep(1000 * time.Millisecond)

	// Now, check if the data is updated after reload.
	result2 := &models.Result{}
	stage.Run("8.8.8.8", result2)
	require.NotNil(t, result2.Geo)
	assert.Equal(t, "Palo Alto", result2.Geo.City)
}
