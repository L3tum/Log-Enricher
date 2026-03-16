package bufferpool

import (
	"log/slog"
	"time"

	"log-enricher/internal/models"
)

// logEntryPool manages a pool of models.LogEntry instances.
type logEntryPool struct {
	pool                   chan *models.LogEntry // Channel for available objects, directly holding interface{} values
	size                   int
	new                    func() *models.LogEntry // Function to create new objects, returning models.LogEntry (interface{})
	tinyFieldCount         uint32                  // Number of tiny log entries (5 fields)
	verySmallFieldCount    uint32                  // Number of very small log entries (6-10 fields)
	smallFieldCount        uint32                  // Number of small log entries (11-20 fields)
	mediumFieldCount       uint32                  // Number of medium log entries (20-40 fields)
	largeFieldCount        uint32                  // Number of large log entries (40-60 fields)
	hugeFieldCount         uint32                  // Number of huge log entries (60+ fields)
	currentIdealFieldCount uint32
	fieldCountChan         chan uint32
}

var LogEntryPool *logEntryPool = newLogEntryPool(100)

// NewLogEntryPool creates a new pool with the specified size.
func newLogEntryPool(size int) *logEntryPool {
	// The channel now holds models.LogEntry (interface{}) directly.
	pool := make(chan *models.LogEntry, size)
	op := &logEntryPool{
		pool:           pool,
		size:           size,
		new:            func() *models.LogEntry { return &models.LogEntry{Fields: make(map[string]interface{}, 20)} },
		fieldCountChan: make(chan uint32, size),
	}

	// Pre-fill the pool with initial objects
	for i := 0; i < size; i++ {
		// Create the object and send it to the channel.
		op.pool <- op.new()
	}

	go op.manageFieldCount()

	return op
}

// Acquire gets an object from the pool. Blocks if no objects are available.
// It returns the models.LogEntry (interface{}) directly.
func (op *logEntryPool) Acquire() *models.LogEntry {
	obj := <-op.pool
	return obj
}

// Release returns an object to the pool.
// It accepts the models.LogEntry (interface{}) directly.
func (op *logEntryPool) Release(obj *models.LogEntry) {
	if obj == nil {
		return
	}

	op.adjustFieldSize(obj)

	obj.Timestamp = time.Time{}
	obj.App = ""

	op.pool <- obj
}

// manageFieldCount is started as a goroutine
// It receives the number of field entries in the log entry and adjusts the optimal size of the fields map accordingly.
func (op *logEntryPool) manageFieldCount() {
	// First adjustment should be pretty soon so in around one minute
	lastAdjustment := time.Now().Add(-9 * time.Minute)

	for count := range op.fieldCountChan {
		if count < 6 {
			op.tinyFieldCount++
		} else if count < 11 {
			op.verySmallFieldCount++
		} else if count < 21 {
			op.smallFieldCount++
		} else if count < 41 {
			op.mediumFieldCount++
		} else if count < 61 {
			op.largeFieldCount++
		} else {
			op.hugeFieldCount++
		}

		// Do an adjustment every 10 minutes
		if lastAdjustment.Before(time.Now().Add(-10 * time.Minute)) {
			lastAdjustment = time.Now()

			maxCount := op.tinyFieldCount * 5
			maxSize := uint32(5)
			idealName := "tiny"

			// Check by number of fields to get a feeling of true size
			if op.verySmallFieldCount*10 > maxCount {
				maxCount = op.verySmallFieldCount * 10
				maxSize = 10
				idealName = "verysmall"
			}
			if op.smallFieldCount*20 > maxCount {
				maxCount = op.smallFieldCount * 20
				maxSize = 20
				idealName = "small"
			}
			if op.mediumFieldCount*40 > maxCount {
				maxCount = op.mediumFieldCount * 40
				maxSize = 40
				idealName = "medium"
			}
			if op.largeFieldCount*60 > maxCount {
				maxCount = op.largeFieldCount * 60
				maxSize = 60
				idealName = "large"
			}
			if op.hugeFieldCount*100 > maxCount {
				// Just keep the current size of the logEntry field
				maxSize = 100
				idealName = "huge"
			}
			op.currentIdealFieldCount = maxSize
			slog.Info("Current ideal field count", "fieldCount", idealName)
			slog.Info("Distribution", "tiny", op.tinyFieldCount, "verySmall", op.verySmallFieldCount, "small", op.smallFieldCount, "medium", op.mediumFieldCount, "large", op.largeFieldCount, "huge", op.hugeFieldCount)

			// Reset sizes
			op.tinyFieldCount = 0
			op.verySmallFieldCount = 0
			op.smallFieldCount = 0
			op.mediumFieldCount = 0
			op.largeFieldCount = 0
			op.hugeFieldCount = 0
		}
	}
}

// adjustFieldSize adjusts the size of the fields map of the log entry.
func (op *logEntryPool) adjustFieldSize(logEntry *models.LogEntry) {
	maxSize := int(op.currentIdealFieldCount)

	// Send the current field count to the channel
	if len(logEntry.Fields) > 0 {
		op.fieldCountChan <- uint32(len(logEntry.Fields))
	}

	// Reduce the size of the fields map if necessary
	// len(fields) - maxSize returns the number of fields that can be removed
	// i.e. that the field count is currently over the current optimal maximum.
	// With the exception of 100, which just is a very large amount of fields and thus
	// we just keep the count as is
	if logEntry.Fields == nil || (len(logEntry.Fields)-maxSize > 0 && maxSize < 100) {
		logEntry.Fields = make(map[string]interface{}, maxSize)
	} else {
		for k := range logEntry.Fields {
			delete(logEntry.Fields, k)
		}
	}
}
