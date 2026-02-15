package bufferpool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogEntryPool(t *testing.T) {
	t.Run("Get and Put clears the map", func(t *testing.T) {
		// Get a map and add data to it
		logEntry := LogEntryPool.Acquire()
		logEntry.Fields["message"] = "hello"
		logEntry.Fields["level"] = "info"
		assert.Len(t, logEntry.Fields, 2)

		// Put it back
		LogEntryPool.Release(logEntry)

		// Get another map (might be the same one)
		logEntry2 := LogEntryPool.Acquire()
		// It should be empty because PutLogEntry clears it
		assert.Len(t, logEntry2.Fields, 0, "map should be empty after being retrieved from pool")
		LogEntryPool.Release(logEntry2)
	})

	t.Run("Concurrency test", func(t *testing.T) {
		// This test is most effective when run with the -race flag
		// go test -race ./...
		var wg sync.WaitGroup
		numGoroutines := 100
		numOperations := 1000

		wg.Add(numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					entry := LogEntryPool.Acquire()
					entry.Fields["key"] = "value"
					assert.Len(t, entry.Fields, 1)
					LogEntryPool.Release(entry)
				}
			}()
		}
		wg.Wait()
	})
}
