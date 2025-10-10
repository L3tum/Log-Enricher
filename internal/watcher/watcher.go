package watcher

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"log-enricher/internal/backends"
	"log-enricher/internal/config"
	"log-enricher/internal/enrichment"
	"log-enricher/internal/state"

	"github.com/fsnotify/fsnotify"
	"github.com/goccy/go-json"
	"github.com/hpcloud/tail"
)

var (
	ipRegex = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)
)

type LogWatcher struct {
	cfg      *config.Config
	enricher enrichment.Enricher
	watcher  *fsnotify.Watcher
	backends backends.Manager
}

func StartLogWatcher(ctx context.Context, cfg *config.Config, enricher enrichment.Enricher, backendManager backends.Manager) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	lw := &LogWatcher{
		cfg:      cfg,
		enricher: enricher,
		watcher:  watcher,
		backends: backendManager,
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
		go lw.tailFile(ctx, file)
	}

	// Watch for new files being created in the directory.
	go lw.watchForNewFiles(ctx)

	return nil
}

func (lw *LogWatcher) tailFile(ctx context.Context, path string) {
	fileState := state.GetOrCreateFileState(path)
	var resumeLine int64 = 0

	// Determine starting position
	startPos := &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd}
	if fileState.LineNumber > 0 {
		if matchedLine, found := state.FindMatchingPosition(path, fileState); found {
			log.Printf("State match for %s, will resume processing after line %d", path, matchedLine)
			resumeLine = matchedLine
			startPos = &tail.SeekInfo{Offset: 0, Whence: io.SeekStart} // Start from beginning to skip lines
		} else {
			log.Printf("State mismatch for %s, processing from beginning.", path)
			startPos = &tail.SeekInfo{Offset: 0, Whence: io.SeekStart}
		}
	} else {
		log.Printf("No state for %s, starting from end.", path)
	}

	tailConfig := tail.Config{
		Location:  startPos,
		ReOpen:    true,                  // Re-open the file if it's rotated (renamed/recreated)
		MustExist: true,                  // Fail if the file doesn't exist initially
		Follow:    true,                  // Follow the file as it grows
		Logger:    tail.DiscardingLogger, // Suppress the library's noisy internal logging
	}

	t, err := tail.TailFile(path, tailConfig)
	if err != nil {
		log.Printf("Error starting to tail file %s: %v", path, err)
		return
	}
	log.Printf("Tailing %s...", path)

	var currentLine int64 = 0
	for {
		select {
		case <-ctx.Done():
			// Context canceled, stop tailing.
			_ = t.Stop()
			return
		case line, ok := <-t.Lines:
			if !ok {
				// Channel closed, tailer has stopped.
				log.Printf("Tailing was stopped for %s, reason: %v", path, t.Err())
				return
			}
			currentLine++

			// Skip lines until we are past the last processed one.
			if currentLine <= resumeLine {
				continue
			}

			lw.processLine(path, line.Text)

			// Update state in memory
			fileState.LineNumber = currentLine
			fileState.LineContent = line.Text
		}
	}
}

func (lw *LogWatcher) processLine(path string, line string) {
	var enrichedEntry map[string]interface{}
	trimmedLine := strings.TrimSpace(line)

	// Fast heuristic: If the line doesn't start with '{', assume it's not a JSON object.
	if len(trimmedLine) > 0 && trimmedLine[0] == '{' {
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(trimmedLine), &logEntry); err == nil {
			// It's valid JSON
			enrichedEntry = lw.enrichJSONLog(logEntry)
		} else {
			// It looked like JSON but wasn't, process as plain text.
			enrichedEntry = lw.enrichPlainTextLog(trimmedLine)
		}
	} else {
		// It's plain text.
		enrichedEntry = lw.enrichPlainTextLog(trimmedLine)
	}

	if enrichedEntry != nil {
		lw.backends.Broadcast(path, enrichedEntry)
	}
}

func (lw *LogWatcher) enrichPlainTextLog(line string) map[string]interface{} {
	logEntry := make(map[string]interface{})
	logEntry["message"] = line

	// Find the first IP in the message
	ips := ipRegex.FindStringSubmatch(line)
	var clientIP string
	if len(ips) > 0 {
		clientIP = ips[0]
	}

	if clientIP != "" {
		lw.applyEnrichmentResult(logEntry, clientIP)
	}

	return logEntry
}

func (lw *LogWatcher) enrichJSONLog(logEntry map[string]interface{}) map[string]interface{} {
	var clientIP string

	// 1. Search for IP in configured fields
	for _, field := range lw.cfg.ClientIPFields {
		if ipVal, ok := logEntry[field]; ok {
			if ip, ok := ipVal.(string); ok && ip != "" {
				clientIP = ip
				break
			}
		}
	}

	// 2. If no IP found, search in "msg" or "message" fields
	if clientIP == "" {
		for _, msgField := range []string{"msg", "message"} {
			if msgVal, ok := logEntry[msgField]; ok {
				if msgStr, ok := msgVal.(string); ok {
					ips := ipRegex.FindStringSubmatch(msgStr)
					if len(ips) > 0 {
						clientIP = ips[0]
						break
					}
				}
			}
		}
	}

	// 3. If an IP was found, enrich and modify the log entry
	if clientIP != "" {
		lw.applyEnrichmentResult(logEntry, clientIP)

		// 4. Delete the original IP fields
		for _, field := range lw.cfg.ClientIPFields {
			if field != "client_ip" {
				delete(logEntry, field)
			}
		}
	}

	return logEntry
}

func (lw *LogWatcher) applyEnrichmentResult(logEntry map[string]interface{}, clientIP string) {
	result := lw.enricher.Enrich(clientIP)
	logEntry["client_ip"] = clientIP
	if result.Hostname != "" {
		logEntry["client_hostname"] = result.Hostname
	}
	if result.Geo != nil && result.Geo.Country != "" {
		logEntry["client_country"] = result.Geo.Country
	}
	if result.Crowdsec != nil && result.Crowdsec.IsBanned {
		logEntry["crowdsec_banned"] = true
		logEntry["crowdsec_decisions"] = result.Crowdsec.Decisions
	}
}

func (lw *LogWatcher) watchForNewFiles(ctx context.Context) {
	// Keep a map of paths we've already started tailing to prevent duplicates from rapid events.
	var mu sync.Mutex
	tailed := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			_ = lw.watcher.Close()
			return
		case event, ok := <-lw.watcher.Events:
			if !ok {
				return
			}
			// Only watch for new files being created.
			if event.Op&fsnotify.Create == fsnotify.Create {
				if matchesAnyExtension(event.Name, lw.cfg.LogFileExtensions) {
					mu.Lock()
					if !tailed[event.Name] {
						log.Printf("Detected new file via directory watch: %s", event.Name)
						go lw.tailFile(ctx, event.Name)
						tailed[event.Name] = true
						// Clean up the map entry after a short delay to allow re-creation.
						time.AfterFunc(5*time.Second, func() {
							mu.Lock()
							delete(tailed, event.Name)
							mu.Unlock()
						})
					}
					mu.Unlock()
				}
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
