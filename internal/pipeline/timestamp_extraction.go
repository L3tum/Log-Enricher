package pipeline

import (
	"fmt"
	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log/slog"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
)

// Common timestamp layouts to try for parsing.
// This list can be extended based on common log formats.
var defaultTimestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000Z07:00", // ISO 8601 with milliseconds and timezone
	"2006-01-02T15:04:05Z07:00",     // ISO 8601 with timezone
	"2006-01-02 15:04:05.000",       // Common format with milliseconds
	"2006-01-02 15:04:05",           // Common format
	"Jan _2 15:04:05",               // Syslog-like without year
	"Jan _2 15:04:05.000",           // Syslog-like with milliseconds
	"2006-01-02 15:04:05Z0700",      // Another common format: "2025-10-09 16:42:13+0000"
}

var defaultTimestampFields = []string{
	"time", "timestamp", "Timestamp", "ts", "date", "created_at", "CreatedAt", "starttime", "StartTime", "@timestamp",
}

// TimestampExtractionStageConfig defines the configuration for the timestamp extraction stage.
type TimestampExtractionStageConfig struct {
	// A list of fields to check in order for the timestamp.
	TimestampFields []string `mapstructure:"timestamp_fields"`
	// Custom timestamp layouts to try in addition to default ones.
	CustomLayouts []string `mapstructure:"custom_layouts"`
}

type timestampCacheData struct {
	field  string
	layout string
}

// TimestampExtractionStage is a pipeline stage that extracts a timestamp
// from a log entry and sets it to the common Timestamp field.
type TimestampExtractionStage struct {
	timestampFields          []string
	layouts                  []string
	mostLikelyTimestampField *cache.PersistedCache[timestampCacheData]
}

// NewTimestampExtractionStage creates a new timestamp extraction stage.
// This stage is typically created programmatically by the pipeline manager
// based on the global config.ParseTimestampEnabled flag.
func NewTimestampExtractionStage(params map[string]interface{}) (Stage, error) {
	var config TimestampExtractionStageConfig

	// Configure mapstructure to handle comma-separated strings as slices.
	decoderConfig := &mapstructure.DecoderConfig{
		Result: &config,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToSliceHookFunc(","), // This hook splits strings by comma into slices
			mapstructure.TextUnmarshallerHookFunc(),
		),
	}

	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapstructure decoder: %w", err)
	}

	if err := decoder.Decode(params); err != nil {
		return nil, fmt.Errorf("failed to decode client ip extraction stage config: %w", err)
	}

	// Trim spaces from each field name after decoding
	for i, field := range config.TimestampFields {
		config.TimestampFields[i] = strings.TrimSpace(field)
	}

	for i, field := range config.CustomLayouts {
		config.CustomLayouts[i] = strings.TrimSpace(field)
	}

	// Combine default and custom timestamp fields
	timestampFields := make([]string, len(defaultTimestampFields)+len(config.TimestampFields))
	copy(timestampFields, defaultTimestampFields)
	copy(timestampFields[len(defaultTimestampFields):], config.TimestampFields)

	// Combine default and custom layouts
	layouts := make([]string, len(defaultTimestampLayouts)+len(config.CustomLayouts))
	copy(layouts, defaultTimestampLayouts)
	copy(layouts[len(defaultTimestampLayouts):], config.CustomLayouts)

	slog.Debug("Timestamp extraction stage enabled", "fields", config.TimestampFields, "layouts", layouts)
	return &TimestampExtractionStage{
		timestampFields:          timestampFields,
		layouts:                  layouts,
		mostLikelyTimestampField: cache.NewPersistedCache[timestampCacheData]("timestamp_extraction_field", 10, 100, false),
	}, nil
}

func (s *TimestampExtractionStage) Name() string {
	return "timestamp_extraction"
}

// Process attempts to extract a timestamp from the log entry.
func (s *TimestampExtractionStage) Process(entry *models.LogEntry) (bool, error) {
	slog.Debug("TimestampExtractionStage: Processing entry", "current_fields", entry.Fields)

	// Add the length of the field map to the cache key to invalidate a negative cache if the timestamp may appear in only some messages
	cacheKey := entry.SourcePath + string(rune(len(entry.Fields)))

	if timestampField, ok := s.mostLikelyTimestampField.Get(cacheKey); ok {
		// If the field is empty, no field was found before and we skip this line
		if timestampField.field == "" {
			s.mostLikelyTimestampField.Hit()
			return true, nil
		}

		if timestamp, ok := s.tryParseTimestamp(&timestampField, entry); ok {
			s.mostLikelyTimestampField.Hit()
			entry.Timestamp = timestamp
			slog.Debug("TimestampExtractionStage: Found timestamp in most likely field", "field", timestampField.field, "layout", timestampField.layout, "timestamp", timestamp)

			return true, nil
		}
	}

	s.mostLikelyTimestampField.Miss()

	timestampField := timestampCacheData{}

	// Try to find timestamp in configured fields.
	for _, field := range s.timestampFields {
		timestampField.field = field
		if timestamp, ok := s.tryParseTimestamp(&timestampField, entry); ok {
			s.mostLikelyTimestampField.Set(cacheKey, timestampField)

			entry.Timestamp = timestamp
			slog.Debug("TimestampExtractionStage: Found timestamp in field", "field", field, "timestamp", timestamp)
			return true, nil
		}
	}

	// If we reach this point, the timestamp was not found in any of the configured fields
	timestampField.field = ""
	s.mostLikelyTimestampField.Set(cacheKey, timestampField)

	slog.Debug("TimestampExtractionStage: No timestamp found after all attempts.")
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	return true, nil // Keep log even if timestamp not found/parsed from fields.
}

func (s *TimestampExtractionStage) tryParseTimestamp(timestampField *timestampCacheData, entry *models.LogEntry) (time.Time, bool) {
	var val interface{}
	var ok bool
	if val, ok = entry.Fields[timestampField.field]; !ok {
		return time.Time{}, false
	}

	var timestampStr string

	if timestampStr, ok = val.(string); !ok {
		return time.Time{}, false
	}

	if timestampField.layout != "" {
		if timestamp, err := time.Parse(timestampField.layout, timestampStr); err == nil && !timestamp.IsZero() {
			return timestamp, true
		}
	}

	for _, layout := range s.layouts {
		if timestamp, err := time.Parse(layout, timestampStr); err == nil && !timestamp.IsZero() {
			timestampField.layout = layout
			slog.Debug("TimestampExtractionStage: Found timestamp in field", "field", timestampField.field, "layout", timestampField.layout, "timestamp", timestamp)

			return timestamp, true
		}
	}

	return time.Time{}, false
}
