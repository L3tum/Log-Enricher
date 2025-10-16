package bufferpool

import (
	"sync"
)

var (
	// pool is a pool of map[string]interface{} objects for parsed log entries.
	logEntryPool = sync.Pool{
		New: func() interface{} {
			// Pre-allocate with a reasonable capacity to avoid resizing for common logs.
			return make(map[string]interface{}, 20)
		},
	}
)

// GetLogEntry retrieves a buffer from the pool.
func GetLogEntry() map[string]interface{} {
	return logEntryPool.Get().(map[string]interface{})
}

func PutLogEntry(logEntry map[string]interface{}) {
	// make sure it's empty before returning it to the pool
	for k := range logEntry {
		delete(logEntry, k)
	}

	logEntryPool.Put(logEntry)
}
