package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"log-enricher/internal/models"
)

// FileState represents the state of a single log file
type FileState struct {
	Path         string `json:"path"`
	LineNumber   int64  `json:"line_number"`
	LineContent  string `json:"line_content"`            // Content of the last processed line
	FileSize     int64  `json:"file_size,omitempty"`     // File size at last read, only saved on shutdown
	LastModified int64  `json:"last_modified,omitempty"` // Unix timestamp, only saved on shutdown
}

// AppState holds all persistent state for the application
type AppState struct {
	Files map[string]*FileState    `json:"files"`
	Cache map[string]models.Result `json:"cache"`
	mu    sync.RWMutex
}

var globalState *AppState

// Initialize creates a new state and loads from disk if available
func Initialize(stateFilePath string) error {
	if stateFilePath == "" {
		return fmt.Errorf("state file path is empty")
	}

	globalState = &AppState{
		Files: make(map[string]*FileState),
		Cache: make(map[string]models.Result),
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

	var tempState struct {
		Files map[string]*FileState    `json:"files"`
		Cache map[string]models.Result `json:"cache"`
	}

	if err := json.Unmarshal(data, &tempState); err != nil {
		// Attempt to handle old state format gracefully
		if err := json.Unmarshal(data, &globalState); err == nil {
			log.Println("Warning: Loaded state from a potentially old format. Consider resetting state if issues occur.")
			return nil
		}
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	if tempState.Files != nil {
		globalState.Files = tempState.Files
	}
	if tempState.Cache != nil {
		globalState.Cache = tempState.Cache
	}

	log.Printf("Loaded state: %d files, %d cache entries", len(globalState.Files), len(globalState.Cache))
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

	log.Printf("Saved state: %d files, %d cache entries", len(globalState.Files), len(globalState.Cache))
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
		info, err := os.Stat(path)
		if err != nil {
			log.Printf("Could not stat file %s for metadata update: %v", path, err)
			delete(globalState.Files, path)
			continue
		}
		fileState.FileSize = info.Size()
		fileState.LastModified = info.ModTime().Unix()
	}
}

// FindMatchingPosition tries to find where we left off.
func FindMatchingPosition(path string, storedState *FileState) (int64, bool) {
	// If we have no line content to match, we have no choice but to start over.
	if storedState.LineContent == "" {
		log.Printf("No last known line content for %s, starting from beginning.", path)
		return 0, false
	}

	file, err := os.Open(path)
	if err != nil {
		log.Printf("Error opening file for position matching %s: %v", path, err)
		return 0, false // Cannot find, so start from beginning
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		log.Printf("Error stating file for position matching %s: %v", path, err)
		return 0, false // Cannot find, so start from beginning
	}

	// Scenario 1: File appears unchanged. Check only if metadata is present in the state.
	if storedState.FileSize > 0 && storedState.LastModified > 0 {
		if info.Size() == storedState.FileSize && info.ModTime().Unix() == storedState.LastModified {
			log.Printf("File %s appears unchanged based on stored metadata. Verifying line %d.", path, storedState.LineNumber)
			scanner := bufio.NewScanner(file)
			var lineNum int64 = 0
			for scanner.Scan() {
				lineNum++
				if lineNum == storedState.LineNumber {
					if scanner.Text() == storedState.LineContent {
						log.Printf("Content of line %d matches. Resuming from this line.", storedState.LineNumber)
						return storedState.LineNumber, true
					}
					log.Printf("Content mismatch at line %d. Will rescan file.", storedState.LineNumber)
					break // Fall through to scenario 2
				}
			}
			if lineNum < storedState.LineNumber {
				log.Printf("File seems truncated. Will rescan file.")
				// Fall through to scenario 2
			}
		}
	} else {
		log.Printf("No existing file metadata for %s, will perform content matching.", path)
	}

	// Scenario 2: File has changed, content mismatched, or no metadata. Search for the line content.
	log.Printf("File %s has changed or content mismatched. Searching for last known line content: \"%s\"", path, truncateForLogging(storedState.LineContent, 256))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		log.Printf("Error rewinding file %s: %v. Starting from beginning.", path, err)
		return 0, false
	}

	scanner := bufio.NewScanner(file)
	var lineNum int64 = 0
	for scanner.Scan() {
		lineNum++
		// Search the entire file for the last known line content.
		// This is safer if the file has been truncated at the beginning.
		if scanner.Text() == storedState.LineContent {
			log.Printf("Found matching content at new line number %d. Resuming from here.", lineNum)
			return lineNum, true
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error scanning file %s for position: %v", path, err)
	}

	// Scenario 3: Not found
	log.Printf("Could not find matching content in %s. Starting from beginning.", path)
	return 0, false
}

// truncateForLogging truncates a string to a max length for safe logging.
func truncateForLogging(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- Cache Functions ---

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
