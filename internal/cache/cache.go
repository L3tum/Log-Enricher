package cache

import (
	"log"
	"log-enricher/internal/models"
	"log-enricher/internal/state"

	lru "github.com/hashicorp/golang-lru/v2"
)

var (
	resultCache *lru.Cache[string, models.Result]
	initialized bool
)

func Initialize(size int) error {
	if initialized {
		return nil
	}

	initialized = true

	cache, err := lru.New[string, models.Result](size)
	if err != nil {
		return err
	}
	resultCache = cache
	return nil
}

func Get(ip string) (models.Result, bool) {
	return resultCache.Get(ip)
}

func Add(ip string, result models.Result) {
	resultCache.Add(ip, result)
}

func Keys() []string {
	// Return keys from LRU cache
	return resultCache.Keys()
}

func LoadFromState() error {
	// Load from unified state
	keys := state.GetAllCacheKeys()
	for _, key := range keys {
		if result, ok := state.GetCacheEntry(key); ok {
			resultCache.Add(key, result)
		}
	}

	// Clear state cache so that it doesn't balloon beyond the CACHE_SIZE
	state.ClearCache()

	log.Printf("Loaded %d entries from state into LRU cache", len(keys))
	return nil
}

func SaveToState() error {
	// Sync LRU cache to state
	keys := resultCache.Keys()

	for _, key := range keys {
		if result, ok := resultCache.Get(key); ok {
			state.SetCacheEntry(key, result)
		}
	}

	log.Printf("Synced %d entries from LRU cache to state", len(keys))
	return nil
}
