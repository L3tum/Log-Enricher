package pipeline

import (
	"bytes"
	"log-enricher/internal/models"
	"testing"
	"time"
)

// Helper to create a LogEntry for testing
func newTestLogEntry(timestamp time.Time, fields map[string]interface{}) *models.LogEntry {
	entry := &models.LogEntry{Fields: fields, Timestamp: timestamp, App: "test"}
	return entry
}

// filterTestCase defines a single test case for the FilterStage.Process method.
type filterTestCase struct {
	name     string
	config   FilterStageConfig
	line     []byte
	entry    *models.LogEntry
	wantKeep bool
}

// runFilterProcessTest is a helper function to execute a single filter test case.
func runFilterProcessTest(t *testing.T, tc filterTestCase) {
	t.Helper() // Marks this function as a test helper

	// Create a map for NewFilterStage parameters from the config struct
	params := make(map[string]interface{})
	if tc.config.Regex != "" {
		params["regex"] = tc.config.Regex
	}
	if tc.config.MinSize > 0 {
		params["min_size"] = tc.config.MinSize
	}
	if tc.config.MaxSize > 0 {
		params["max_size"] = tc.config.MaxSize
	}
	if tc.config.MaxAge > 0 {
		params["max_age"] = tc.config.MaxAge
	}
	if tc.config.JSONField != "" {
		params["json_field"] = tc.config.JSONField
	}
	if tc.config.JSONValue != "" {
		params["json_value"] = tc.config.JSONValue
	}
	params["action"] = tc.config.Action
	params["match"] = tc.config.Match

	s, err := NewFilterStage(params)
	if err != nil {
		t.Fatalf("NewFilterStage() error = %v", err)
	}

	tc.entry.LogLine = tc.line

	gotKeep, err := s.Process(tc.entry)
	if err != nil {
		t.Errorf("Process() error = %v", err)
		return
	}
	if gotKeep != tc.wantKeep {
		t.Errorf("Process() gotKeep = %v, want %v", gotKeep, tc.wantKeep)
	}
}

func TestNewFilterStage(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]interface{}
		wantErr bool
	}{
		{
			name:    "Valid empty config",
			params:  map[string]interface{}{},
			wantErr: false,
		},
		{
			name: "Valid regex config",
			params: map[string]interface{}{
				"regex": "error",
			},
			wantErr: false,
		},
		{
			name: "Invalid regex config",
			params: map[string]interface{}{
				"regex": "[", // Invalid regex
			},
			wantErr: true,
		},
		{
			name: "Valid JSON field regex config",
			params: map[string]interface{}{
				"json_field": "level",
				"json_value": "error",
			},
			wantErr: false,
		},
		{
			name: "Invalid JSON field regex config",
			params: map[string]interface{}{
				"json_field": "level",
				"json_value": "[", // Invalid regex
			},
			wantErr: true,
		},
		{
			name: "Valid MaxAge config",
			params: map[string]interface{}{
				"max_age": 3600,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFilterStage(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewFilterStage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestFilterStage_Process_Regex(t *testing.T) {
	testLine := []byte("This is an ERROR log line.")
	testEntry := newTestLogEntry(time.Now(), nil)

	tests := []filterTestCase{
		{
			name: "Drop on regex match (default action)",
			config: FilterStageConfig{
				Regex:  "ERROR",
				Action: "drop",
				Match:  "any",
			},
			line:     testLine,
			entry:    testEntry,
			wantKeep: false, // Rule returns true, so dropped
		},
		{
			name: "Keep on regex match",
			config: FilterStageConfig{
				Regex:  "ERROR",
				Action: "keep",
				Match:  "any",
			},
			line:     testLine,
			entry:    testEntry,
			wantKeep: true, // Rule returns true, so kept
		},
		{
			name: "Keep on no regex match (default action)",
			config: FilterStageConfig{
				Regex:  "WARNING",
				Action: "drop",
				Match:  "any",
			},
			line:     testLine,
			entry:    testEntry,
			wantKeep: true, // Rule returns false, so kept
		},
		{
			name: "Drop on no regex match",
			config: FilterStageConfig{
				Regex:  "WARNING",
				Action: "keep",
				Match:  "any",
			},
			line:     testLine,
			entry:    testEntry,
			wantKeep: false, // Rule returns false, so dropped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFilterProcessTest(t, tt)
		})
	}
}

func TestFilterStage_Process_MinMaxSize(t *testing.T) {
	shortLine := []byte("short")               // len = 5
	longLine := bytes.Repeat([]byte("a"), 100) // len = 100
	testEntry := newTestLogEntry(time.Now(), nil)

	tests := []filterTestCase{
		{
			name: "Drop if too short (MinSize=10, line=5)",
			config: FilterStageConfig{
				MinSize: 10,
				Action:  "drop",
				Match:   "any",
			},
			line:     shortLine,
			entry:    testEntry,
			wantKeep: false, // Rule returns true (too short), so dropped
		},
		{
			name: "Drop if too long (MaxSize=10, line=100)",
			config: FilterStageConfig{
				MaxSize: 10,
				Action:  "drop",
				Match:   "any",
			},
			line:     longLine,
			entry:    testEntry,
			wantKeep: false, // Rule returns true (too long), so dropped
		},
		{
			name: "Keep if within size (MinSize=10, MaxSize=200, line=100)",
			config: FilterStageConfig{
				MinSize: 10,
				MaxSize: 200,
				Action:  "drop",
				Match:   "any",
			},
			line:     longLine,
			entry:    testEntry,
			wantKeep: true, // Rule returns false (within size), so kept
		},
		{
			name: "Keep if too short (MinSize=10, line=5) with keep action",
			config: FilterStageConfig{
				MinSize: 10,
				Action:  "keep",
				Match:   "any",
			},
			line:     shortLine,
			entry:    testEntry,
			wantKeep: true, // Rule returns true (too short), so kept
		},
		{
			name: "Keep if too long (MaxSize=10, line=100) with keep action",
			config: FilterStageConfig{
				MaxSize: 10,
				Action:  "keep",
				Match:   "any",
			},
			line:     longLine,
			entry:    testEntry,
			wantKeep: true, // Rule returns true (too long), so kept
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFilterProcessTest(t, tt)
		})
	}
}

func TestFilterStage_Process_MaxAge(t *testing.T) {
	now := time.Now()
	recentEntry := newTestLogEntry(now.Add(-30*time.Minute), nil) // 30 mins ago
	oldEntry := newTestLogEntry(now.Add(-2*time.Hour), nil)       // 2 hours ago

	tests := []filterTestCase{
		{
			name: "Drop old log (MaxAge=1h, log=2h old)",
			config: FilterStageConfig{
				MaxAge: 3600, // 1 hour
				Action: "drop",
				Match:  "any",
			},
			entry:    oldEntry,
			wantKeep: false, // Rule returns true (is old), so dropped
		},
		{
			name: "Keep recent log (MaxAge=1h, log=30m old)",
			config: FilterStageConfig{
				MaxAge: 3600, // 1 hour
				Action: "drop",
				Match:  "any",
			},
			entry:    recentEntry,
			wantKeep: true, // Rule returns false (not old), so kept
		},
		{
			name: "Keep old log (MaxAge=1h, log=2h old) with keep action",
			config: FilterStageConfig{
				MaxAge: 3600, // 1 hour
				Action: "keep",
				Match:  "any",
			},
			entry:    oldEntry,
			wantKeep: true, // Rule returns true (is old), so kept
		},
		{
			name: "Drop recent log (MaxAge=1h, log=30m old) with keep action",
			config: FilterStageConfig{
				MaxAge: 3600, // 1 hour
				Action: "keep",
				Match:  "any",
			},
			entry:    recentEntry,
			wantKeep: false, // Rule returns false (not old), so dropped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFilterProcessTest(t, tt)
		})
	}
}

func TestFilterStage_Process_JSONField(t *testing.T) {
	jsonEntry := newTestLogEntry(time.Now(), map[string]interface{}{
		"level":   "error",
		"message": "something went wrong",
	})
	nonJsonEntry := newTestLogEntry(time.Now(), nil)

	tests := []filterTestCase{
		{
			name: "Drop on JSON field match (level=error)",
			config: FilterStageConfig{
				JSONField: "level",
				JSONValue: "error",
				Action:    "drop",
				Match:     "any",
			},
			entry:    jsonEntry,
			wantKeep: false, // Rule returns true, so dropped
		},
		{
			name: "Keep on JSON field match (level=error)",
			config: FilterStageConfig{
				JSONField: "level",
				JSONValue: "error",
				Action:    "keep",
				Match:     "any",
			},
			entry:    jsonEntry,
			wantKeep: true, // Rule returns true, so kept
		},
		{
			name: "Keep on no JSON field match (level=info)",
			config: FilterStageConfig{
				JSONField: "level",
				JSONValue: "info",
				Action:    "drop",
				Match:     "any",
			},
			entry:    jsonEntry,
			wantKeep: true, // Rule returns false, so kept
		},
		{
			name: "Drop on no JSON field match (level=info) with keep action",
			config: FilterStageConfig{
				JSONField: "level",
				JSONValue: "info",
				Action:    "keep",
				Match:     "any",
			},
			entry:    jsonEntry,
			wantKeep: false, // Rule returns false, so dropped
		},
		{
			name: "Keep if JSON field not present",
			config: FilterStageConfig{
				JSONField: "non_existent",
				JSONValue: "value",
				Action:    "drop",
				Match:     "any",
			},
			entry:    jsonEntry,
			wantKeep: true, // Rule returns false, so kept
		},
		{
			name: "Keep if entry is nil",
			config: FilterStageConfig{
				JSONField: "level",
				JSONValue: "error",
				Action:    "drop",
				Match:     "any",
			},
			entry:    nonJsonEntry, // entry.Fields is nil
			wantKeep: true,         // Rule returns false, so kept
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFilterProcessTest(t, tt)
		})
	}
}

func TestFilterStage_Process_MultipleRules_AnyMatch(t *testing.T) {
	now := time.Now()
	recentErrorLine := []byte("This is a recent ERROR log.") // len = 27
	oldInfoLine := []byte("This is an old INFO log.")        // len = 24
	recentInfoLine := []byte("This is a recent INFO log.")   // len = 26

	recentErrorEntry := newTestLogEntry(now.Add(-10*time.Minute), nil)
	oldInfoEntry := newTestLogEntry(now.Add(-2*time.Hour), nil)
	recentInfoEntry := newTestLogEntry(now.Add(-10*time.Minute), nil)

	tests := []filterTestCase{
		{
			name: "Drop if any match: Regex 'ERROR' OR MaxAge=1h (recent error)",
			config: FilterStageConfig{
				Regex:  "ERROR",
				MaxAge: 3600, // 1 hour
				Action: "drop",
				Match:  "any",
			},
			line:     recentErrorLine,
			entry:    recentErrorEntry,
			wantKeep: false, // Matches Regex, so dropped
		},
		{
			name: "Drop if any match: Regex 'ERROR' OR MaxAge=1h (old info)",
			config: FilterStageConfig{
				Regex:  "ERROR",
				MaxAge: 3600, // 1 hour
				Action: "drop",
				Match:  "any",
			},
			line:     oldInfoLine,
			entry:    oldInfoEntry,
			wantKeep: false, // Matches MaxAge (is old), so dropped
		},
		{
			name: "Keep if no match: Regex 'ERROR' OR MaxAge=1h (recent info)",
			config: FilterStageConfig{
				Regex:  "ERROR",
				MaxAge: 3600, // 1 hour
				Action: "drop",
				Match:  "any",
			},
			line:     recentInfoLine,
			entry:    recentInfoEntry,
			wantKeep: true, // No match, so kept
		},
		{
			name: "Keep if any match: Regex 'ERROR' OR MaxAge=1h (recent error) with keep action",
			config: FilterStageConfig{
				Regex:  "ERROR",
				MaxAge: 3600, // 1 hour
				Action: "keep",
				Match:  "any",
			},
			line:     recentErrorLine,
			entry:    recentErrorEntry,
			wantKeep: true, // Matches Regex, so kept
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFilterProcessTest(t, tt)
		})
	}
}

func TestFilterStage_Process_MultipleRules_AllMatch(t *testing.T) {
	now := time.Now()
	// Line 1: Long and old (matches both conditions)
	longOldLine := bytes.Repeat([]byte("a"), 600) // len = 600
	longOldEntry := newTestLogEntry(now.Add(-2*time.Hour), nil)

	// Line 2: Long but recent (matches only MaxSize)
	longRecentLine := bytes.Repeat([]byte("b"), 600) // len = 600
	longRecentEntry := newTestLogEntry(now.Add(-30*time.Minute), nil)

	// Line 3: Short but old (matches only MaxAge)
	shortOldLine := []byte("short line") // len = 10
	shortOldEntry := newTestLogEntry(now.Add(-2*time.Hour), nil)

	// Line 4: Short and recent (matches neither)
	shortRecentLine := []byte("another short line") // len = 18
	shortRecentEntry := newTestLogEntry(now.Add(-30*time.Minute), nil)

	tests := []filterTestCase{
		{
			name: "Drop if ALL match: MaxSize=512 AND MaxAge=1h (long and old)",
			config: FilterStageConfig{
				MaxSize: 512,  // Rule returns true if > 512
				MaxAge:  3600, // Rule returns true if > 1h old
				Action:  "drop",
				Match:   "all",
			},
			line:     longOldLine,
			entry:    longOldEntry,
			wantKeep: false, // Both rules match, so dropped
		},
		{
			name: "Keep if ALL match: MaxSize=512 AND MaxAge=1h (long but recent)",
			config: FilterStageConfig{
				MaxSize: 512,
				MaxAge:  3600,
				Action:  "drop",
				Match:   "all",
			},
			line:     longRecentLine,
			entry:    longRecentEntry,
			wantKeep: true, // MaxAge rule returns false (not old), so not an "all" match, thus kept
		},
		{
			name: "Keep if ALL match: MaxSize=512 AND MaxAge=1h (short but old)",
			config: FilterStageConfig{
				MaxSize: 512,
				MaxAge:  3600,
				Action:  "drop",
				Match:   "all",
			},
			line:     shortOldLine,
			entry:    shortOldEntry,
			wantKeep: true, // MaxSize rule returns false (not too long), so not an "all" match, thus kept
		},
		{
			name: "Keep if ALL match: MaxSize=512 AND MaxAge=1h (short and recent)",
			config: FilterStageConfig{
				MaxSize: 512,
				MaxAge:  3600,
				Action:  "drop",
				Match:   "all",
			},
			line:     shortRecentLine,
			entry:    shortRecentEntry,
			wantKeep: true, // Neither rule matches, so not an "all" match, thus kept
		},
		{
			name: "Keep if ALL match: MaxSize=512 AND MaxAge=1h (long and old) with keep action",
			config: FilterStageConfig{
				MaxSize: 512,
				MaxAge:  3600,
				Action:  "keep",
				Match:   "all",
			},
			line:     longOldLine,
			entry:    longOldEntry,
			wantKeep: true, // Both rules match, so kept
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFilterProcessTest(t, tt)
		})
	}
}
