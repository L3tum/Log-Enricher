package backends

import (
	"fmt"
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

// Send marshals the entry to JSON and writes it to a corresponding .enriched file.
// It uses an optimistic, lock-free read for existing writers.
func (b *FileBackend) Send(sourcePath string, entry map[string]interface{}) error {
	writer, err := b.getWriter(sourcePath)
	if err != nil {
		return err
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	_, err = writer.Write(line)
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

// Shutdown closes all open file writers managed by the backend.
func (b *FileBackend) Shutdown() {
	// Iterate over the sync.Map and close each writer.
	b.writers.Range(func(key, value interface{}) bool {
		if writer, ok := value.(*os.File); ok {
			_ = writer.Close()
		}
		// The map does not need to be cleared, as the instance will be discarded.
		return true
	})
}
