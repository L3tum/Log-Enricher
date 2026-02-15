package state

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"log-enricher/internal/models"
)

// FileState represents the state of a single log file
type FileState struct {
	Path         string `json:"path"`
	LineNumber   int64  `json:"line_number"`
	Inode        uint64 `json:"inode,omitempty"`         // Inode number for file identity
	FileSize     int64  `json:"file_size,omitempty"`     // File size at last read, only saved on shutdown
	LastModified int64  `json:"last_modified,omitempty"` // Unix timestamp, only saved on shutdown
	mu           sync.RWMutex
}

func (f *FileState) IncrementLineNumber() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LineNumber++
}

func (f *FileState) GetLineNumber() int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.LineNumber
}

// AppState holds all persistent state for the application
type AppState struct {
	Files  map[string]*FileState     `json:"files"`
	Caches map[string]map[string]any `json:"caches"`
	Cache  map[string]models.Result  `json:"cache"`
	mu     sync.RWMutex
}

var globalState *AppState

// Initialize creates a new state and loads from disk if available
func Initialize(stateFilePath string) error {
	if stateFilePath == "" {
		return fmt.Errorf("state file path is empty")
	}

	globalState = &AppState{
		Files:  make(map[string]*FileState),
		Caches: make(map[string]map[string]any),
		Cache:  make(map[string]models.Result),
	}

	return Load(stateFilePath)
}

// Load reads state from disk
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("No existing state file found, starting fresh")
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	globalState.mu.Lock()
	defer globalState.mu.Unlock()

	if err := json.Unmarshal(data, &globalState); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	slog.Info("Loaded state", "files", len(globalState.Files), "cacheEntries", len(globalState.Cache))
	return nil
}

// Save writes state to disk
func Save(path string) error {
	if path == "" {
		return fmt.Errorf("state file path is empty")
	}

	UpdateAllFileMetadata() // Update metadata just before saving

	globalState.mu.RLock()
	data, err := json.MarshalIndent(globalState, "", "  ")
	globalState.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	slog.Info("Saved state", "files", len(globalState.Files), "cacheEntries", len(globalState.Cache))
	return nil
}

// --- File State Functions ---

// GetOrCreateFileState gets the existing state for a file or creates it if it doesn't exist.
// This function locks the global state to ensure thread-safe access to the map.
func GetOrCreateFileState(path string) *FileState {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()

	state, ok := globalState.Files[path]
	if !ok {
		state = &FileState{Path: path}
		globalState.Files[path] = state
	}
	return state
}

// UpdateAllFileMetadata iterates through all known files and updates their size and mod time.
// This is intended to be called only on graceful shutdown.
func UpdateAllFileMetadata() {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()

	log.Println("Updating file metadata before shutdown...")
	for path, fileState := range globalState.Files {
		fileState.mu.Lock()
		info, err := os.Stat(path)
		if err != nil {
			slog.Error("Could not stat file for metadata update", "path", path, "error", err)
			fileState.mu.Unlock()
			delete(globalState.Files, path)
			continue
		}
		fileState.FileSize = info.Size()
		fileState.LastModified = info.ModTime().Unix()
		if inode, ok := getInode(info); ok {
			fileState.Inode = inode
		}
		fileState.mu.Unlock()
	}
}

// FindMatchingPosition determines if we can resume tailing based on file metadata.
// It returns the line number to resume from and a boolean indicating if a match was found.
func FindMatchingPosition(path string, storedState *FileState) (int64, bool) {
	storedState.mu.RLock()
	defer storedState.mu.RUnlock()
	info, err := os.Stat(path)
	if err != nil {
		slog.Error("Error stating file for position matching. Starting from beginning.", "path", path, "error", err)
		return 0, false
	}

	// Primary check: Inode. If it doesn't match, it's a different file.
	currentInode, inodeSupported := getInode(info)
	if inodeSupported && storedState.Inode != 0 && currentInode != storedState.Inode {
		slog.Info("File has been rotated (inode mismatch). Starting from beginning.", "path", path, "stored", storedState.Inode, "current", currentInode)
		return 0, false
	}

	// If inodes match, we only need to check for truncation.
	if inodeSupported && storedState.Inode != 0 && currentInode == storedState.Inode {
		if info.Size() < storedState.FileSize {
			slog.Info("File was truncated (same inode, size is smaller). Starting from beginning.", "path", path)
			return 0, false
		}

		// If size is unchanged but modification time changed, we cannot safely
		// assume this is the same logical file (inode reuse after rotation can
		// happen on some filesystems). Be conservative and restart.
		if storedState.FileSize > 0 && info.Size() == storedState.FileSize &&
			storedState.LastModified > 0 && info.ModTime().Unix() != storedState.LastModified {
			slog.Info("File metadata changed with same size/inode. Starting from beginning.", "path", path)
			return 0, false
		}

		return storedState.LineNumber, true
	}

	// Fallback for when inode is not supported or it's the first run with the new state format.
	// We check for strict equality to ensure it's the same file.
	if storedState.FileSize == 0 || storedState.LastModified == 0 {
		return 0, false // No prior state to compare against.
	}
	if info.Size() == storedState.FileSize && info.ModTime().Unix() == storedState.LastModified {
		return storedState.LineNumber, true
	}

	slog.Info("File has changed. Starting from beginning.", "path", path)
	return 0, false
}

// --- Cache Functions ---

func GetCacheEntries(name string) (map[string]any, bool) {
	globalState.mu.RLock()
	defer globalState.mu.RUnlock()
	result, ok := globalState.Caches[name]
	return result, ok
}

func SetCacheEntries(name string, entries map[string]any) {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	globalState.Caches[name] = entries
}

func GetCacheEntry(ip string) (models.Result, bool) {
	result, ok := globalState.Cache[ip]
	return result, ok
}

func SetCacheEntry(ip string, result models.Result) {
	globalState.Cache[ip] = result
}

func GetAllCacheKeys() []string {
	keys := make([]string, 0, len(globalState.Cache))
	for k := range globalState.Cache {
		keys = append(keys, k)
	}
	return keys
}

func GetCacheSize() int {
	return len(globalState.Cache)
}

func ClearCache() {
	globalState.Cache = make(map[string]models.Result)
}
