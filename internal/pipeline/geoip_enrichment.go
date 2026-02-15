package pipeline

import (
	"context"
	"fmt"
	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mitchellh/mapstructure"
	"github.com/oschwald/geoip2-golang"
)

// GeoIpConfig holds the configuration for the GeoIP enrichment stage.
type GeoIpConfig struct {
	ClientIpField      string `mapstructure:"client_ip_field"`
	ClientCountryField string `mapstructure:"client_country_field"`
	DatabasePath       string `mapstructure:"geoip_database_path"`
}

type GeoIpEnrichmentStage struct {
	config            *GeoIpConfig
	db                *geoip2.Reader
	mu                sync.RWMutex // Protects db
	clientIpToCountry *cache.PersistedCache[string]
	openDB            func(string) (*geoip2.Reader, error)
}

func (s *GeoIpEnrichmentStage) Name() string {
	return "geoip enrichment"
}

func (s *GeoIpEnrichmentStage) Process(entry *models.LogEntry) (keep bool, err error) {
	clientIp, ok := entry.Fields[s.config.ClientIpField]

	if !ok {
		return true, nil
	}

	clientIP, ok := clientIp.(string)

	if !ok {
		return true, nil
	}

	ip := net.ParseIP(clientIP)
	if ip == nil {
		return true, nil
	}

	// Skip private IPs, loopbacks, and link-local addresses
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true, nil
	}

	if country, ok := s.clientIpToCountry.Get(clientIP); ok {
		s.clientIpToCountry.Hit()
		entry.Fields[s.config.ClientCountryField] = country
	} else {
		s.clientIpToCountry.Miss()
		s.mu.RLock()
		db := s.db
		s.mu.RUnlock()
		if db == nil {
			slog.Warn("GeoIP: Database unavailable, skipping lookup", "ip", clientIP)
			return true, nil
		}

		country, err := db.Country(ip)
		if err != nil {
			slog.Error("GeoIP: Error looking up country", "ip", clientIP, "error", err)
			return true, nil
		}
		entry.Fields[s.config.ClientCountryField] = country.Country.IsoCode
		s.clientIpToCountry.Set(clientIP, country.Country.IsoCode)
	}

	return true, nil
}

// NewGeoIPStage creates a new stage for GeoIP enrichment.
// It returns an error if the database cannot be opened.
// It returns a nil stage and nil error if the path is not configured.
// It starts a background goroutine to watch for database updates.
func NewGeoIPStage(ctx context.Context, params map[string]any) (Stage, error) {
	var stageConfig GeoIpConfig
	if err := mapstructure.Decode(params, &stageConfig); err != nil {
		return nil, fmt.Errorf("failed to decode geoip enrichment stage config: %w", err)
	}

	if stageConfig.ClientIpField == "" {
		stageConfig.ClientIpField = "client_ip"
	}

	if stageConfig.ClientCountryField == "" {
		stageConfig.ClientCountryField = "client_country"
	}

	if stageConfig.DatabasePath == "" {
		slog.Warn("GeoIP database path is not configured.")
		return nil, nil
	}

	if _, err := os.Stat(stageConfig.DatabasePath); os.IsNotExist(err) {
		slog.Error("GeoIP database file not found", "file", stageConfig.DatabasePath, "error", err)
		return nil, err
	}

	db, err := geoip2.Open(stageConfig.DatabasePath)
	if err != nil {
		slog.Error("Failed to open GeoIP database", "file", stageConfig.DatabasePath, "error", err)
		return nil, err
	}

	stage := &GeoIpEnrichmentStage{
		config:            &stageConfig,
		db:                db,
		clientIpToCountry: cache.NewPersistedCache[string]("geoip_enrichment", 100, 1000, true),
		openDB:            geoip2.Open,
	}

	go stage.watchForUpdates(ctx)
	slog.Info("GeoIP enrichment stage initialized, watching for database updates.")
	return stage, nil
}

func (s *GeoIpEnrichmentStage) watchForUpdates(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("GeoIP: Error creating file watcher", "error", err)
		return
	}
	defer watcher.Close()

	// Watch the directory, as file updates can sometimes be atomic renames.
	dir := filepath.Dir(s.config.DatabasePath)
	if err = watcher.Add(dir); err != nil {
		slog.Error("GeoIP: Error adding watcher for directory", "directory", dir, "error", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Name == s.config.DatabasePath && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Rename == fsnotify.Rename) {
				slog.Info("GeoIP: Database file appears to have been updated. Reloading.", "file", s.config.DatabasePath)
				s.reloadDB()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("GeoIP: Watcher error", "error", err)
			return
		}
	}

}

// reloadDB handles the logic of swapping the GeoIP database.
func (s *GeoIpEnrichmentStage) reloadDB() {
	open := s.openDB
	if open == nil {
		open = geoip2.Open
	}

	var newDb *geoip2.Reader
	var err error
	const maxRetries = 5
	const retryDelay = 100 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		newDb, err = open(s.config.DatabasePath)
		if err == nil {
			break // Success
		}

		// Check for the specific sharing violation error text on Windows.
		if strings.Contains(err.Error(), "used by another process") {
			slog.Warn("GeoIP: Database is locked, retrying", "delay", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		// It's a different kind of error, so we shouldn't retry.
		break
	}

	if err != nil {
		slog.Error("GeoIP: Error reloading database after multiple retries", "file", s.config.DatabasePath, "error", err)
		return
	}

	s.mu.Lock()
	oldDb := s.db
	s.db = newDb
	s.mu.Unlock()

	if oldDb != nil {
		if closeErr := oldDb.Close(); closeErr != nil {
			slog.Error("GeoIP: Error closing old database", "error", closeErr)
		}
	}

	slog.Info("GeoIP: Database reloaded successfully.")
}
