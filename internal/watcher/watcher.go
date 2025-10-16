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

	"github.com/fsnotify/fsnotify"
	"github.com/goccy/go-json"
)

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
	var whence = io.SeekEnd
	if fileState.LineNumber > 0 {
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
		log.Printf("No state for %s, starting from end.", path)
	}

	t := tailer.NewTailer(path, offset, whence)
	t.Start()
	log.Printf("Tailing %s...", path)

	var currentLine int64 = 0
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

			currentLine++

			lw.processLine(path, line.Buffer)

			// Ensure the buffer is returned to the pool after we're done with it.
			bufferpool.PutByteBuffer(line.Buffer)

			// Update state in memory.
			fileState.LineNumber = currentLine
		}
	}
}

func (lw *LogWatcher) processLine(path string, line *bytes.Buffer) {
	logEntry := bufferpool.GetLogEntry()
	defer bufferpool.PutLogEntry(logEntry)

	lineBytes := line.Bytes()
	plainText := false

	// Fast heuristic: If the line doesn't start with '{', assume it's not a JSON object.
	if len(lineBytes) > 0 && lineBytes[0] == '{' && json.Unmarshal(lineBytes, &logEntry) == nil {
		// Do nothing it's JSON and been processed by Unmarshal
	} else {
		// It's plain text.
		logEntry["message"] = line.String()
		plainText = true
	}

	// Run through pipeline
	if (plainText && lw.cfg.PlaintextProcessingEnabled) || !plainText {
		drop, enrichedLogEntry := lw.manager.Process(lineBytes, logEntry)
		if drop == true {
			// Drop the line if it was dropped by the pipeline
			return
		}
		lw.backends.Broadcast(path, enrichedLogEntry)
	} else {
		lw.backends.Broadcast(path, logEntry)
	}
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
