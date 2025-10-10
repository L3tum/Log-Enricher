package backends

// Backend is the interface for all output destinations.
type Backend interface {
	// Send transmits the log entry to the backend.
	// sourcePath is the path of the original log file being processed.
	Send(sourcePath string, entry map[string]interface{}) error
	// Shutdown gracefully closes the backend connection or flushes buffers.
	Shutdown()
	// Name returns the descriptive name of the backend.
	Name() string
}
