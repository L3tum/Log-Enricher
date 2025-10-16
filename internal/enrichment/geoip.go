package enrichment

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log-enricher/internal/models"

	"github.com/fsnotify/fsnotify"
	"github.com/oschwald/geoip2-golang"
)

// GeoIpConfig holds the configuration for the GeoIP enrichment stage.
type GeoIpConfig struct {
	DatabasePath string `mapstructure:"database_path"`
}

// GeoIPStage enriches an IP with geo-location information.
// It can reload the GeoIP database if it's updated on disk.
type GeoIPStage struct {
	config *GeoIpConfig
	db     *geoip2.Reader
	mu     sync.RWMutex
}

// NewGeoIPStage creates a new stage for GeoIP enrichment.
// It returns an error if the database cannot be opened.
// It returns a nil stage and nil error if the path is not configured.
// It starts a background goroutine to watch for database updates.
func NewGeoIPStage(config *GeoIpConfig) (*GeoIPStage, error) {
	if config.DatabasePath == "" {
		log.Println("GeoIP database path is not configured.")
		return nil, nil
	}

	if _, err := os.Stat(config.DatabasePath); os.IsNotExist(err) {
		return nil, err
	}

	db, err := geoip2.Open(config.DatabasePath)
	if err != nil {
		return nil, err
	}

	stage := &GeoIPStage{
		config: config,
		db:     db,
	}

	go stage.watchForUpdates()
	log.Println("GeoIP enrichment stage initialized, watching for database updates.")
	return stage, nil
}

// Name returns the name of the stage.
func (s *GeoIPStage) Name() string {
	return "GeoIP"
}

// Run performs the enrichment, modifying the result in place.
func (s *GeoIPStage) Run(ipStr string, result *models.Result) (updated bool) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// Skip private IPs, as they are not useful for geolocation.
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsInterfaceLocalMulticast() {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false
	}

	record, err := s.db.City(ip)
	if err != nil {
		// This can happen for private IPs, not necessarily an error.
		return false
	}

	updated = false
	needsCountryUpdate := record.Country.IsoCode != "" && (result.Geo == nil || result.Geo.Country != record.Country.IsoCode)
	cityName, cityExists := record.City.Names["en"]
	needsCityUpdate := cityExists && cityName != "" && (result.Geo == nil || result.Geo.City != cityName)

	if needsCountryUpdate || needsCityUpdate {
		if result.Geo == nil {
			result.Geo = &models.GeoInfo{}
		}
		if needsCountryUpdate {
			result.Geo.Country = record.Country.IsoCode
			updated = true
		}
		if needsCityUpdate {
			result.Geo.City = cityName
			updated = true
		}
	}

	return updated
}

func (s *GeoIPStage) watchForUpdates() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("GeoIP: Error creating file watcher: %v", err)
		return
	}
	defer watcher.Close()

	// Watch the directory, as file updates can sometimes be atomic renames.
	dir := filepath.Dir(s.config.DatabasePath)
	if err := watcher.Add(dir); err != nil {
		log.Printf("GeoIP: Error adding watcher for directory %s: %v", dir, err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Name == s.config.DatabasePath && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Rename == fsnotify.Rename) {
				log.Printf("GeoIP: Database file %s appears to have been updated. Reloading.", s.config.DatabasePath)
				s.reloadDB()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("GeoIP: Watcher error: %v", err)
			return
		}
	}

}

// reloadDB handles the logic of swapping the GeoIP database.
func (s *GeoIPStage) reloadDB() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// First, close the existing database to release the file lock before opening a new one.
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			log.Printf("GeoIP: Error closing old database: %v", err)
		}
		s.db = nil
	}

	// Add a retry loop to handle transient file locks on Windows.
	var newDb *geoip2.Reader
	var err error
	const maxRetries = 5
	const retryDelay = 100 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		newDb, err = geoip2.Open(s.config.DatabasePath)
		if err == nil {
			break // Success
		}

		// Check for the specific sharing violation error text on Windows.
		if strings.Contains(err.Error(), "used by another process") {
			log.Printf("GeoIP: Database is locked, retrying in %v...", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		// It's a different kind of error, so we shouldn't retry.
		break
	}

	if err != nil {
		log.Printf("GeoIP: Error reloading database %s after retries: %v", s.config.DatabasePath, err)
		return
	}

	s.db = newDb
	log.Println("GeoIP: Database reloaded successfully.")
}
