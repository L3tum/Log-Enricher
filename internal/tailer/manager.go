package tailer

import (
	"context"
	"fmt"
	"io"
	"log-enricher/internal/backends"
	"log-enricher/internal/config"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/processor"
	"log-enricher/internal/state"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Manager interface {
	StartWatching(ctx context.Context) error
}

type ManagerImpl struct {
	cfg                  *config.Config
	appIdentification    *regexp.Regexp
	watcher              *fsnotify.Watcher
	mu                   sync.Mutex // Protects tailedFiles
	tailedFiles          map[string]context.CancelFunc
	pm                   pipeline.Manager
	bb                   backends.Backend
	ignoredLogFilesRegex *regexp.Regexp
}

func NewManagerImpl(cfg *config.Config, pm pipeline.Manager, bb backends.Backend) (*ManagerImpl, error) {
	manager := &ManagerImpl{
		cfg:         cfg,
		tailedFiles: make(map[string]context.CancelFunc),
		pm:          pm,
		bb:          bb,
	}

	// Compile app identification regex if provided
	if cfg.AppIdentificationRegex != "" {
		re, err := regexp.Compile(cfg.AppIdentificationRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid AppIdentificationRegex: %w", err)
		}
		// Check for named capture group "app"
		foundAppGroup := false
		for _, name := range re.SubexpNames() {
			if name == "app" {
				foundAppGroup = true
				break
			}
		}
		if !foundAppGroup {
			return nil, fmt.Errorf("AppIdentificationRegex must contain a named capture group 'app'")
		}
		manager.appIdentification = re
		slog.Info("App identification regex enabled", "regex", cfg.AppIdentificationRegex)
	} else if cfg.AppName != "" {
		slog.Info("Static app name configured", "app_name", cfg.AppName)
	} else {
		slog.Info("No app identification configured. Will use directory name as app name.")
	}

	if cfg.LogFilesIgnored != "" {
		re, err := regexp.Compile(cfg.LogFilesIgnored)
		if err != nil {
			return nil, fmt.Errorf("invalid LogFilesIgnored: %w", err)
		}
		manager.ignoredLogFilesRegex = re
		slog.Info("Log files ignored regex enabled", "regex", cfg.LogFilesIgnored)
	}

	fileWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}
	manager.watcher = fileWatcher

	return manager, nil
}

func (m *ManagerImpl) StartWatching(ctx context.Context) error {
	// Watch for new files being created in the directory.
	go m.watch(ctx)

	// Add base path to watcher to discover new files.
	// We also need to add all existing subdirectories to the watcher
	// to detect files created within them.
	err := filepath.WalkDir(m.cfg.LogBasePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			slog.Error("error accessing path during initial directory walk", "path", path, "error", err)
			return nil // Don't stop the walk for individual errors
		}
		if d.IsDir() {
			if err := m.watcher.Add(path); err != nil {
				slog.Error("Failed to add directory to watcher during startup", "path", path, "error", err)
			} else {
				slog.Debug("Added directory to watcher", "path", path)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to initialize watcher for log directories: %w", err)
	}

	// Initial scan for existing files
	files, err := m.getMatchingLogFiles(m.cfg.LogBasePath)
	if err != nil {
		return fmt.Errorf("failed to get matching log files: %w", err)
	}

	for _, file := range files {
		m.startTailingFile(ctx, filepath.Clean(file))
	}

	return nil
}

func (m *ManagerImpl) watch(ctx context.Context) {
	defer m.watcher.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			cleanedEventName := filepath.Clean(event.Name)

			if event.Op&fsnotify.Create == fsnotify.Create {
				// Differentiate between file and directory creation.
				info, err := os.Stat(cleanedEventName)
				if err != nil {
					slog.Error("Failed to stat created item", "path", cleanedEventName, "error", err)
					continue
				}

				if info.IsDir() {
					// Add new directory to watcher.
					slog.Info("New directory created, adding to watcher", "path", cleanedEventName)
					if err := m.watcher.Add(cleanedEventName); err != nil {
						slog.Error("Failed to add new directory to watcher", "path", cleanedEventName, "error", err)
						// Continue, as we might still want to process files if they appear later
					}

					// Scan new directories for existing files.
					// This handles cases where files might be copied into the directory
					// immediately after its creation, or if the directory already contained files.
					newFiles, err := m.getMatchingLogFiles(cleanedEventName)
					if err != nil {
						slog.Error("Failed to scan new directory for log files", "path", cleanedEventName, "error", err)
						continue
					}

					for _, file := range newFiles {
						m.startTailingFile(ctx, filepath.Clean(file))
					}
				} else if m.matchesAnyExtension(cleanedEventName) {
					m.startTailingFile(ctx, cleanedEventName)
				}
			} else if event.Op&fsnotify.Remove == fsnotify.Remove {
				m.stopTailingFile(cleanedEventName)
			} else if event.Op&fsnotify.Rename == fsnotify.Rename {
				// A rename is treated as a removal of the old file name.
				// The new file name will trigger a CREATE event if it's created within the watched directory.
				m.stopTailingFile(cleanedEventName)
			}
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("Watcher error", "error", err)
		}
	}
}

func (m *ManagerImpl) startTailingFile(parentCtx context.Context, path string) {
	if m.ignoredLogFilesRegex != nil && m.ignoredLogFilesRegex.MatchString(path) {
		slog.Info("Ignoring file due to regex", "path", path)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Path is already cleaned by the caller.
	if _, exists := m.tailedFiles[path]; exists {
		// Already tailing this file, do nothing.
		return
	}

	slog.Info("Starting to tail file", "path", path)
	tailerCtx, cancel := context.WithCancel(parentCtx)
	m.tailedFiles[path] = cancel

	go func() {
		defer m.stopTailingFile(path)
		m.tailFile(tailerCtx, path)
	}()
}

func (m *ManagerImpl) stopTailingFile(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Path is already cleaned by the caller.
	if cancel, exists := m.tailedFiles[path]; exists {
		slog.Info("Stopping tail for file due to file system event", "path", path)
		cancel()
		delete(m.tailedFiles, path)
	}
}

func (m *ManagerImpl) tailFile(ctx context.Context, path string) {
	fileState := state.GetOrCreateFileState(path)

	var offset int64 = 0
	var whence = io.SeekStart // Default to SeekStart to process all existing content

	if fileState.GetLineNumber() > 0 {
		if matchedLine, found := state.FindMatchingPosition(path, fileState); found {
			slog.Info("State match for file", "path", path, "resume_after_line", matchedLine)
			offset = matchedLine
		} else {
			slog.Info("State mismatch for file, processing from beginning", "path", path)
			offset = 0
		}
	} else {
		slog.Info("No state for file, starting from beginning", "path", path)
	}

	t := NewTailer(ctx, path, offset, whence)
	t.Start()
	slog.Info("Starting tailer", "path", path)
	appName := m.getAppNameForPath(path)
	slog.Debug("Determined app name for file", "path", path, "app_name", appName)
	lp := processor.NewLogProcessor(appName, path, m.pm.GetProcessPipeline(path), m.bb)

	for {
		select {
		case <-ctx.Done():
			// Context canceled, stop tailing.
			return
		case err, ok := <-t.Errors:
			if !ok {
				return
			}
			slog.Error("Error tailing file", "path", path, "error", err)
			return
		case line, ok := <-t.Lines:
			if !ok {
				// Channel closed, tailer has stopped.
				slog.Info("Tailing was stopped for file", "path", path)
				return
			}

			if err := lp.ProcessLine(line.Buffer); err != nil {
				slog.Error("Failed to process line, continuing", "path", path, "error", err)
			}

			// Update state in memory.
			fileState.IncrementLineNumber()
		}
	}
}

func (m *ManagerImpl) getMatchingLogFiles(filePath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			slog.Error("error accessing path during file walk", "path", path, "error", err)
			return nil
		}
		if !d.IsDir() && m.matchesAnyExtension(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory %s: %w", filePath, err)
	}
	return files, nil
}

func (m *ManagerImpl) matchesAnyExtension(filename string) bool {
	for _, ext := range m.cfg.LogFileExtensions {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

// getAppNameForPath determines the application name for a given log file path.
// It uses the configured AppName, AppIdentificationRegex, or falls back to the directory name.
func (m *ManagerImpl) getAppNameForPath(sourcePath string) string {
	var appName string
	if m.cfg.AppName != "" {
		appName = m.cfg.AppName
	} else if m.appIdentification != nil {
		matches := m.appIdentification.FindStringSubmatch(sourcePath)
		if len(matches) > 0 {
			for i, name := range m.appIdentification.SubexpNames() {
				if name == "app" && i < len(matches) {
					appName = matches[i]
					break
				}
			}
		}
	}

	if appName == "" {
		// Fallback to directory name, similar to Loki's previous logic
		dirName := filepath.Base(filepath.Dir(sourcePath))
		if dirName == "." || dirName == "/" || dirName == "" || dirName == "\\" {
			appName = "log-enricher" // Default if directory name is not useful
		} else {
			appName = dirName
		}
	}

	return appName
}
