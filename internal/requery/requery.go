package requery

import (
	"log"
	"log-enricher/internal/models"
	"log-enricher/internal/pipeline"
	"reflect"
	"time"

	"log-enricher/internal/cache"
)

// StartRequeryLoop periodically re-runs enrichment on all items in the cache.
func StartRequeryLoop(interval time.Duration, enrichmentStages []pipeline.EnrichmentStage) {
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
			var newResult models.Result
			for _, stage := range enrichmentStages {
				err := stage.PerformEnrichment(ip, &newResult)
				if err != nil {
					return
				}
			}

			// Only update if the result has meaningfully changed.
			if !reflect.DeepEqual(cachedResult, newResult) {
				cache.Add(ip, newResult)
				log.Printf("Updated cache for %s: %+v", ip, newResult)
			}
		}

		log.Println("Cache requery cycle complete")
	}
}
