package enrichment

import (
	"log"
	"net"
	"path/filepath"
	"sync"

	"log-enricher/internal/models"

	"github.com/fsnotify/fsnotify"
	"github.com/oschwald/geoip2-golang"
)

// GeoIPStage enriches an IP with geo-location information.
// It can reload the GeoIP database if it's updated on disk.
type GeoIPStage struct {
	db     *geoip2.Reader
	dbPath string
	mu     sync.RWMutex
}

// NewGeoIPStage creates a new stage for GeoIP enrichment.
// It returns an error if the database cannot be opened.
// It returns a nil stage and nil error if the path is not configured.
// It starts a background goroutine to watch for database updates.
func NewGeoIPStage(dbPath string) (Stage, error) {
	if dbPath == "" {
		log.Println("GeoIP database path is not configured, stage disabled.")
		return nil, nil // Not an error, just disabled.
	}
	db, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, err
	}

	stage := &GeoIPStage{
		db:     db,
		dbPath: dbPath,
	}

	go stage.watchForDBUpdates()
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

	// The database might be nil if the initial load failed or it is being reloaded.
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

// watchForDBUpdates monitors the GeoIP database file for changes and reloads it.
func (s *GeoIPStage) watchForDBUpdates() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("GeoIP: Failed to create file watcher: %v", err)
		return
	}
	defer watcher.Close()

	// Watch the directory, as file updates can sometimes be atomic renames.
	dir := filepath.Dir(s.dbPath)
	if err := watcher.Add(dir); err != nil {
		log.Printf("GeoIP: Failed to watch directory %s: %v", dir, err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Name == s.dbPath && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Rename == fsnotify.Rename) {
				log.Printf("GeoIP: Database file %s appears to have been updated. Reloading.", s.dbPath)
				s.reloadDB()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("GeoIP: Watcher error: %v", err)
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
	}

	newDb, err := geoip2.Open(s.dbPath)
	if err != nil {
		log.Printf("GeoIP: Error reloading database %s: %v", s.dbPath, err)
		s.db = nil // Ensure we don't use a stale or invalid database.
		return
	}

	s.db = newDb
	log.Println("GeoIP: Database reloaded successfully.")
}
