package bufferpool

import (
	"sync"
	"time"
)

type LogEntry struct {
	Fields    map[string]interface{}
	Timestamp time.Time
}

const INITIAL_FIELD_COUNT = 30
const MAX_FIELD_COUNT = 40

// MAX_IN_FLIGHT_LOG_ENTRIES defines the maximum number of LogEntry objects that can be
// "in-flight" (i.e., checked out from the pool) at any given time.
// This prevents unbounded memory growth under high load.
const MAX_IN_FLIGHT_LOG_ENTRIES = 5000

func NewLogEntry() LogEntry {
	return LogEntry{
		// Pre-allocate with a reasonable capacity to avoid resizing for common logs.
		Fields:    make(map[string]interface{}, INITIAL_FIELD_COUNT),
		Timestamp: time.Now(),
	}
}

func (l LogEntry) ClearFields() {
	for k := range l.Fields {
		delete(l.Fields, k)
	}
}

var (
	logEntryPool = sync.Pool{
		New: func() interface{} {
			return NewLogEntry()
		},
	}

	// logEntrySemaphore is a channel used as a semaphore to limit the number
	// of in-flight log entries.
	logEntrySemaphore = make(chan struct{}, MAX_IN_FLIGHT_LOG_ENTRIES)
)

// GetLogEntry retrieves a LogEntry from the pool. It will block if the maximum
// number of in-flight log entries has been reached.
func GetLogEntry() LogEntry {
	// Acquire a token from the semaphore. This will block if the channel is full.
	logEntrySemaphore <- struct{}{}
	return logEntryPool.Get().(LogEntry)
}

func PutLogEntry(logEntry LogEntry) {
	<-logEntrySemaphore

	// make sure we don't put too many Fields into the pool'
	if len(logEntry.Fields) > MAX_FIELD_COUNT {
		return
	}

	// make sure it's empty before returning it to the pool
	logEntry.ClearFields()
	logEntryPool.Put(logEntry)
}
