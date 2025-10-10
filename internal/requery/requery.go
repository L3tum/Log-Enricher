package requery

import (
	"log"
	"reflect"
	"time"

	"log-enricher/internal/cache"
	"log-enricher/internal/enrichment"
)

// StartRequeryLoop periodically re-runs enrichment on all items in the cache.
func StartRequeryLoop(interval time.Duration, enricher enrichment.Enricher) {
	if interval == 0 {
		return // Requery is disabled.
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Starting cache requery cycle...")
		// Get all keys currently in the LRU cache for requery.
		keys := cache.Keys()

		for _, key := range keys {
			ip := key
			cachedResult, ok := cache.Get(ip)
			if !ok {
				// Item might have been evicted between getting keys and getting the item.
				continue
			}

			// Perform a fresh enrichment, bypassing the cache for the lookup itself.
			newResult := enricher.PerformEnrichment(ip)

			// Only update if the result has meaningfully changed.
			if !reflect.DeepEqual(cachedResult, newResult) {
				cache.Add(ip, newResult)
				log.Printf("Updated cache for %s: %+v", ip, newResult)
			}
		}

		log.Println("Cache requery cycle complete")
	}
}
