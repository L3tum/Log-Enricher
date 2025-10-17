package logging

import (
	"fmt"
	"io"
	"log"
	"log-enricher/internal/bufferpool"
	"os"
	"strings"

	"log-enricher/internal/backends"
)

// dualLogger is an io.Writer that duplicates log output to stdout and a backend manager.
type dualLogger struct {
	stdout   io.Writer
	backends backends.Manager
}

// New creates and configures a new logger that hijacks the standard log package.
func New(backendManager backends.Manager) {
	logger := &dualLogger{
		stdout:   os.Stdout,
		backends: backendManager,
	}
	// Replace the standard logger's output with our custom writer.
	log.SetOutput(logger)
	// Remove standard logger's default prefixes (date/time), as we will control the format.
	log.SetFlags(0)
}

// Write implements the io.Writer interface. It's called by the log package for each log event.
func (d *dualLogger) Write(p []byte) (n int, err error) {
	// 1. Write the original message to stdout.
	n, err = d.stdout.Write(p)
	if err != nil {
		// If we can't even write to stdout, we can't do much else.
		return n, fmt.Errorf("failed to write to stdout: %w", err)
	}

	// 2. Format as a structured log and send to backends.
	// The source path "internal" is used to distinguish app logs from processed logs.
	logEntry := bufferpool.GetLogEntry()
	defer bufferpool.PutLogEntry(logEntry)
	logEntry.Fields["level"] = "info"
	logEntry.Fields["message"] = strings.TrimSpace(string(p))
	logEntry.Fields["source"] = "internal"

	d.backends.Broadcast("internal", logEntry)

	return n, nil
}
