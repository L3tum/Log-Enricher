package backends

import (
	"log-enricher/internal/models"
)

// Backend is the interface for all output destinations.
type Backend interface {
	// Send transmits the log entry to the backend.
	// sourcePath is the path of the original log file being processed.
	// entryAsBytes is the pre-marshaled JSON log entry.
	Send(entry *models.LogEntry) error
	// Shutdown gracefully closes the backend connection or flushes buffers.
	Shutdown()
	// Name returns the descriptive name of the backend.
	Name() string
	// CloseWriter signals the backend that a specific sourcePath is no longer being processed.
	// This allows the backend to release any resources (e.g., file handles) associated with that path.
	CloseWriter(sourcePath string)
}
