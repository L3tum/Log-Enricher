package processor

import (
	"log-enricher/internal/backends"
	"log-enricher/internal/bufferpool"
	"log-enricher/internal/pipeline"
	"log/slog"
	"time"
)

type LogProcessor interface {
	ProcessLine(line []byte) error
}

type LogProcessorImpl struct {
	appName    string
	sourcePath string
	pipeline   pipeline.ProcessPipeline
	backend    backends.Backend
}

func NewLogProcessor(appName string, sourcePath string, processPipeline pipeline.ProcessPipeline, bb backends.Backend) *LogProcessorImpl {
	return &LogProcessorImpl{
		appName:    appName,
		sourcePath: sourcePath,
		pipeline:   processPipeline,
		backend:    bb,
	}
}

func (p *LogProcessorImpl) ProcessLine(line []byte) error {
	slog.Debug("Processing line", "path", p.sourcePath)
	// Acquire a *models.LogEntry
	logEntry := bufferpool.LogEntryPool.Acquire()
	// Ensure the acquired object is released back to the pool when done.
	defer bufferpool.LogEntryPool.Release(logEntry)

	logEntry.LogLine = line
	logEntry.SourcePath = p.sourcePath
	logEntry.App = p.appName

	slog.Debug("Parsed line", "path", p.sourcePath, "fields", logEntry.Fields)

	// Run through pipeline
	keep := p.pipeline.Process(logEntry) // Pass the pointer
	if !keep {
		// Drop the line if it was dropped by the pipeline
		slog.Debug("Dropped line by pipeline", "path", p.sourcePath)
		return nil
	}

	slog.Debug("Processed line through pipeline", "path", p.sourcePath)

	// Fallback: If timestamp is still zero after pipeline processing (e.g., if
	// ParseTimestampEnabled was false, or no timestamp field was found/parsed),
	// set it to the current time
	if logEntry.Timestamp.IsZero() {
		logEntry.Timestamp = time.Now()
	}

	err := p.backend.Send(logEntry) // Pass the pointer
	if err != nil {
		slog.Error("Failed to send log entry to backend", "error", err, "source_path", logEntry.SourcePath, "app", logEntry.App)
	}

	return err
}
