package bufferpool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogEntryPool(t *testing.T) {
	t.Run("Get and Put clears the map", func(t *testing.T) {
		// Get a map and add data to it
		logEntry := GetLogEntry()
		logEntry.Fields["message"] = "hello"
		logEntry.Fields["level"] = "info"
		assert.Len(t, logEntry.Fields, 2)

		// Put it back
		PutLogEntry(logEntry)

		// Get another map (might be the same one)
		logEntry2 := GetLogEntry()
		// It should be empty because PutLogEntry clears it
		assert.Len(t, logEntry2.Fields, 0, "map should be empty after being retrieved from pool")
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
					entry := GetLogEntry()
					entry.Fields["key"] = "value"
					assert.Len(t, entry.Fields, 1)
					PutLogEntry(entry)
				}
			}()
		}
		wg.Wait()
	})
}
