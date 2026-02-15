package backends

import (
	"fmt"
	"log-enricher/internal/models"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/goccy/go-json"
)

// FileBackend writes enriched logs to separate files based on the original log's path.
type FileBackend struct {
	suffix string
	// writers is a map from sourcePath to *os.File. It is concurrency-safe.
	writers sync.Map
}

// NewFileBackend creates a new file-writing backend.
func NewFileBackend(suffix string) *FileBackend {
	return &FileBackend{
		suffix: suffix,
	}
}

func (b *FileBackend) Name() string {
	return "file"
}

// Send writes the pre-marshaled entry to a corresponding .enriched file.
// The incoming byte slice already contains a newline.
func (b *FileBackend) Send(entry *models.LogEntry) error {
	writer, err := b.getWriter(entry.SourcePath)
	if err != nil {
		return err
	}

	// If there are no fields (no JSON) send the log line as the log message
	if len(entry.Fields) == 0 {
		if len(entry.LogLine) == 0 {
			_, err = writer.Write([]byte{'\n'})
			return err
		}
		_, err = writer.Write(entry.LogLine)
		if err != nil {
			return err
		}
		if entry.LogLine[len(entry.LogLine)-1] != '\n' {
			_, err = writer.Write([]byte{'\n'})
		}
		return err
	}

	// Use json.MarshalNoEscape with json.Unordered() to prevent key sorting.
	buf, err := json.MarshalWithOption(entry.Fields, json.UnorderedMap())

	if err != nil {
		return err
	}

	// Add a newline character after each JSON log entry for readability in the file.
	buf = append(buf, '\n')

	_, err = writer.Write(buf)
	return err
}

func (b *FileBackend) getWriter(sourcePath string) (*os.File, error) {
	// Optimistic path (lock-free): check if writer already exists.
	if writer, ok := b.writers.Load(sourcePath); ok {
		return writer.(*os.File), nil
	}

	// Slow path: writer does not exist. We need to create it.
	newWriter, err := b.createWriter(sourcePath)
	if err != nil {
		return nil, err
	}

	// Atomically store the new writer. LoadOrStore returns the existing value if one was stored
	// by a concurrent goroutine, or our new value if we won the race.
	actualWriter, loaded := b.writers.LoadOrStore(sourcePath, newWriter)

	// If another goroutine created the writer in the meantime, close our redundant one.
	if loaded {
		_ = newWriter.Close()
	}

	return actualWriter.(*os.File), nil
}

func (b *FileBackend) createWriter(sourcePath string) (*os.File, error) {
	outputPath := sourcePath + b.suffix
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory for enriched file %s: %w", outputPath, err)
	}

	f, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open enriched file %s: %w", outputPath, err)
	}
	return f, nil
}

// CloseWriter closes the file writer for a specific sourcePath and removes it from the map.
func (b *FileBackend) CloseWriter(sourcePath string) {
	if writer, loaded := b.writers.LoadAndDelete(sourcePath); loaded {
		if file, ok := writer.(*os.File); ok {
			slog.Info("Closing enriched log file", "path", sourcePath+b.suffix)
			if err := file.Close(); err != nil {
				slog.Error("Failed to close enriched log file", "path", sourcePath+b.suffix, "error", err)
			}
		}
	}
}

// Shutdown closes all open file writers managed by the backend.
func (b *FileBackend) Shutdown() {
	// Iterate over the sync.Map and close each writer.
	b.writers.Range(func(key, value interface{}) bool {
		if writer, ok := value.(*os.File); ok {
			slog.Info("Closing enriched log file during shutdown", "path", key.(string)+b.suffix)
			if err := writer.Close(); err != nil {
				slog.Error("Failed to close enriched log file during shutdown", "path", key.(string)+b.suffix, "error", err)
			}
		}
		// The map does not need to be cleared explicitly, as the instance will be discarded.
		return true
	})
}
