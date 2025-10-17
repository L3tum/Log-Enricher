package watcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log-enricher/internal/backends"
	"log-enricher/internal/bufferpool"
	"log-enricher/internal/config"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/state"
	"log-enricher/internal/tailer"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/goccy/go-json"
)

// Common timestamp layouts to try for parsing.
// This list can be extended based on common log formats.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000Z07:00", // ISO 8601 with milliseconds and timezone
	"2006-01-02T15:04:05Z07:00",     // ISO 8601 with timezone
	"2006-01-02 15:04:05.000",       // Common format with milliseconds
	"2006-01-02 15:04:05",           // Common format
	"Jan _2 15:04:05",               // Syslog-like without year
	"Jan _2 15:04:05.000",           // Syslog-like with milliseconds
}

type LogWatcher struct {
	cfg         *config.Config
	watcher     *fsnotify.Watcher
	manager     pipeline.Manager
	backends    backends.Manager
	mu          sync.Mutex
	tailedFiles map[string]context.CancelFunc
}

func StartLogWatcher(ctx context.Context, cfg *config.Config, manager pipeline.Manager, backendManager backends.Manager) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	lw := &LogWatcher{
		cfg:         cfg,
		manager:     manager,
		watcher:     watcher,
		backends:    backendManager,
		tailedFiles: make(map[string]context.CancelFunc),
	}

	// Add base path to watcher to discover new files.
	if err := watcher.Add(cfg.LogBasePath); err != nil {
		return fmt.Errorf("failed to watch log directory: %w", err)
	}

	// Initial scan for existing files
	files, err := getMatchingLogFiles(cfg.LogBasePath, cfg.LogFileExtensions)
	if err != nil {
		return fmt.Errorf("failed to get matching log files: %w", err)
	}

	for _, file := range files {
		lw.startTailingFile(ctx, file)
	}

	// Watch for new files being created in the directory.
	go lw.watch(ctx)

	return nil
}

func (lw *LogWatcher) tailFile(ctx context.Context, path string) {
	fileState := state.GetOrCreateFileState(path)

	// Determine starting position
	var offset int64 = 0
	var whence = io.SeekStart // Default to SeekStart to process all existing content

	if fileState.GetLineNumber() > 0 {
		if matchedLine, found := state.FindMatchingPosition(path, fileState); found {
			log.Printf("State match for %s, will resume processing after line %d", path, matchedLine)
			offset = matchedLine + 1
			whence = io.SeekStart // Start from beginning to skip lines
		} else {
			log.Printf("State mismatch for %s, processing from beginning.", path)
			offset = 0
			whence = io.SeekStart
		}
	} else {
		log.Printf("No state for %s, starting from beginning.", path)
		// offset and whence are already 0 and io.SeekStart by default
	}

	t := tailer.NewTailer(path, offset, whence)
	t.Start()
	log.Printf("Tailing %s...", path)

	for {
		select {
		case <-ctx.Done():
			// Context canceled, stop tailing.
			t.Stop()
			return
		case err, ok := <-t.Errors:
			if !ok {
				return
			}
			log.Printf("Error tailing file %s: %v", path, err)
			return
		case line, ok := <-t.Lines:
			if !ok {
				// Channel closed, tailer has stopped.
				log.Printf("Tailing was stopped for %s", path)
				return
			}

			lw.processLine(path, line.Buffer)

			// Update state in memory.
			fileState.IncrementLineNumber()
		}
	}
}

func (lw *LogWatcher) processLine(path string, line *bytes.Buffer) {
	// Ensure the buffer is returned to the pool after we're done with it.
	defer bufferpool.PutByteBuffer(line)
	logEntry := bufferpool.GetLogEntry()
	defer bufferpool.PutLogEntry(logEntry)

	lineBytes := line.Bytes()
	plainText := false

	// Fast heuristic: If the line doesn't start with '{', assume it's not a JSON object.
	if len(lineBytes) > 0 && lineBytes[0] == '{' && json.Unmarshal(lineBytes, &logEntry.Fields) == nil {
		// Do nothing it's JSON and been processed by Unmarshal
	} else {
		// It's plain text.
		logEntry.Fields["message"] = line.String()
		plainText = true
	}

	if !lw.cfg.ParseTimestampEnabled || !lw.ExtractTimestampToCommonField(&logEntry) {
		logEntry.Timestamp = time.Now()
	}

	// Run through pipeline
	if (plainText && lw.cfg.PlaintextProcessingEnabled) || !plainText {
		drop := lw.manager.Process(lineBytes, &logEntry)
		if drop == true {
			// Drop the line if it was dropped by the pipeline
			return
		}
		lw.backends.Broadcast(path, logEntry)
	} else {
		lw.backends.Broadcast(path, logEntry)
	}
}

func (lw *LogWatcher) ExtractTimestampToCommonField(logEntry *bufferpool.LogEntry) bool {
	for _, field := range lw.cfg.TimestampFields {
		if val, ok := logEntry.Fields[field]; ok {
			if timestampStr, isString := val.(string); isString && timestampStr != "" {
				for _, layout := range timestampLayouts {
					if t, err := time.Parse(layout, timestampStr); err == nil {
						logEntry.Timestamp = t
						return true
					}
				}
				// If parsing failed for all layouts, log a warning but continue.
				log.Printf("Warning: Could not parse timestamp '%s' from field '%s' using known layouts.", timestampStr, field)
			}
		}
	}

	return false
}

func (lw *LogWatcher) startTailingFile(parentCtx context.Context, path string) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if _, exists := lw.tailedFiles[path]; exists {
		// Already tailing this file, do nothing.
		return
	}

	log.Printf("Starting to tail file: %s", path)
	tailerCtx, cancel := context.WithCancel(parentCtx)
	lw.tailedFiles[path] = cancel

	go func() {
		lw.tailFile(tailerCtx, path)
		// Once tailFile returns, remove it from the map.
		lw.mu.Lock()
		delete(lw.tailedFiles, path)
		lw.mu.Unlock()
	}()
}

func (lw *LogWatcher) stopTailingFile(path string) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if cancel, exists := lw.tailedFiles[path]; exists {
		log.Printf("Stopping tail for %s due to file system event.", path)
		cancel()
		delete(lw.tailedFiles, path)
	}
}

func (lw *LogWatcher) watch(ctx context.Context) {
	defer lw.watcher.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-lw.watcher.Events:
			if !ok {
				return
			}

			// Handle only events for files with matching extensions.
			if !matchesAnyExtension(event.Name, lw.cfg.LogFileExtensions) {
				continue
			}

			if event.Op&fsnotify.Create == fsnotify.Create {
				lw.startTailingFile(ctx, event.Name)
			} else if event.Op&fsnotify.Remove == fsnotify.Remove {
				lw.stopTailingFile(event.Name)
			} else if event.Op&fsnotify.Rename == fsnotify.Rename {
				// A rename is treated as a removal of the old file name.
				// The new file name will trigger a CREATE event if it's created within the watched directory.
				lw.stopTailingFile(event.Name)
			}
		case err, ok := <-lw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func matchesAnyExtension(filename string, extensions []string) bool {
	for _, ext := range extensions {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

func getMatchingLogFiles(basePath string, logFileExtensions []string) ([]string, error) {
	var files []string
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("error accessing path %s during file walk: %v", path, err)
			return nil
		}
		if !info.IsDir() && matchesAnyExtension(path, logFileExtensions) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory %s: %w", basePath, err)
	}
	return files, nil
}
