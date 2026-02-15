package cache

import (
	"log-enricher/internal/state"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

type PersistedCache[T any] struct {
	name      string
	mu        sync.RWMutex // Protects cache
	cache     map[string]T
	hits      atomic.Uint64
	misses    atomic.Uint64
	maxSize   int
	persisted bool
}

func NewPersistedCache[T any](name string, initialSize int, maxSize int, persisted bool) *PersistedCache[T] {
	c := &PersistedCache[T]{
		name:      name,
		cache:     make(map[string]T, initialSize),
		maxSize:   maxSize,
		persisted: persisted,
	}
	c.LoadFromState()
	go c.manage()
	return c
}

func (c *PersistedCache[T]) Has(key string) bool {
	_, ok := c.cache[key]
	return ok
}

func (c *PersistedCache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.cache[key]
	return v, ok
}

func (c *PersistedCache[T]) Set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = value
}

func (c *PersistedCache[T]) Delete(key string) {
	if !c.Has(key) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, key)
}

func (c *PersistedCache[T]) Hit() {
	c.hits.Add(1)
}

func (c *PersistedCache[T]) Miss() {
	c.misses.Add(1)
}

func (c *PersistedCache[T]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

func (c *PersistedCache[T]) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.cache))
	for k := range c.cache {
		keys = append(keys, k)
	}
	return keys
}

// Copy provides a copy of the internal cache data
func (c *PersistedCache[T]) Copy() map[string]T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copied := make(map[string]T, len(c.cache))
	for k, v := range c.cache {
		copied[k] = v
	}
	return copied
}

func (c *PersistedCache[T]) manage() {
	// Sleep between 30 and 300 530 to introduce some jitter since we expect multiple cache instances
	numberOfSeconds := rand.IntN(530-30) + 30
	time.Sleep(time.Duration(numberOfSeconds) * time.Second)
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		length := c.Len()
		hits := c.hits.Swap(0)
		misses := c.misses.Swap(0)
		slog.Info("Cache Stats", "size", length, "hits", hits, "misses", misses, "cacheName", c.name)

		// Just delete the oldest entries if the cache is too big
		if length > c.maxSize {
			keys := c.Keys()
			c.mu.Lock()
			for i := 0; i < length-c.maxSize; i++ {
				delete(c.cache, keys[i])
			}
			c.mu.Unlock()
			slog.Info("Trimmed cache", "size", length, "max", c.maxSize, "removed", length-c.maxSize, "cacheName", c.name)
		}

		c.PersistToState()
	}
}

func (c *PersistedCache[T]) PersistToState() {
	if !c.persisted {
		return
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	stateCached := make(map[string]any, len(c.cache))
	for k, v := range c.cache {
		stateCached[k] = v
	}

	state.SetCacheEntries(c.name, stateCached)
	slog.Info("Persisted cache to state", "cacheName", c.name)
}

func (c *PersistedCache[T]) LoadFromState() {
	if !c.persisted {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	stateCached, ok := state.GetCacheEntries(c.name)

	if !ok {
		return
	}

	for k, v := range stateCached {
		c.cache[k] = v.(T)
	}
	slog.Info("Loaded cache from state", "cacheName", c.name)
}
