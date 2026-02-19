package pipeline

import (
	"log-enricher/internal/bufferpool"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimestampExtractionStageProcess(t *testing.T) {
	testCases := []struct {
		name                   string
		timestampFields        []string
		customLayouts          []string
		inputEntryFields       map[string]interface{}
		initialTimestamp       time.Time
		expectedTimestamp      time.Time
		expectTimestampToBeNow bool
		expectError            bool
	}{
		{
			name:              "JSON with RFC3339 timestamp field",
			timestampFields:   []string{"time"},
			inputEntryFields:  map[string]interface{}{"time": "2023-01-01T12:34:56Z", "message": "event"},
			expectedTimestamp: time.Date(2023, time.January, 1, 12, 34, 56, 0, time.UTC),
		},
		{
			name:              "JSON with different timestamp field and milliseconds format",
			timestampFields:   []string{"log_timestamp"},
			inputEntryFields:  map[string]interface{}{"log_timestamp": "2023-03-15 10:00:00.123", "level": "INFO"},
			expectedTimestamp: time.Date(2023, time.March, 15, 10, 0, 0, 123000000, time.UTC),
		},
		{
			name:              "JSON with new '2006-01-02 15:04:05Z0700' format",
			timestampFields:   []string{"ts"},
			inputEntryFields:  map[string]interface{}{"ts": "2025-10-09 16:42:13+0000", "event": "test"},
			expectedTimestamp: time.Date(2025, time.October, 9, 16, 42, 13, 0, time.FixedZone("", 0)), // +0000 means UTC
		},
		{
			name:                   "JSON with unparseable timestamp, falls back to now",
			timestampFields:        []string{"ts"},
			inputEntryFields:       map[string]interface{}{"ts": "not-a-timestamp", "message": "bad log"},
			expectTimestampToBeNow: true,
		},
		{
			name:                   "No timestamp field present, falls back to now",
			timestampFields:        []string{"non_existent_field"},
			inputEntryFields:       map[string]interface{}{"message": "no timestamp here"},
			expectTimestampToBeNow: true,
		},
		{
			name:              "Custom layout provided",
			timestampFields:   []string{"custom_time"},
			customLayouts:     []string{"02/01/2006 15:04:05"},
			inputEntryFields:  map[string]interface{}{"custom_time": "15/06/2024 11:22:33"},
			expectedTimestamp: time.Date(2024, time.June, 15, 11, 22, 33, 0, time.UTC),
		},
		{
			name:                   "Empty timestamp string, falls back to now",
			timestampFields:        []string{"time"},
			inputEntryFields:       map[string]interface{}{"time": "", "message": "empty time"},
			expectTimestampToBeNow: true,
		},
		{
			name:                   "Timestamp field is not a string, falls back to now",
			timestampFields:        []string{"time"},
			inputEntryFields:       map[string]interface{}{"time": 12345, "message": "int time"},
			expectTimestampToBeNow: true,
		},
		{
			name:              "No parseable timestamp keeps existing timestamp",
			timestampFields:   []string{"time"},
			inputEntryFields:  map[string]interface{}{"time": "not-a-timestamp", "message": "keep existing"},
			initialTimestamp:  time.Date(2026, time.April, 8, 9, 10, 11, 0, time.UTC),
			expectedTimestamp: time.Date(2026, time.April, 8, 9, 10, 11, 0, time.UTC),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			stageConfig := TimestampExtractionStageConfig{
				TimestampFields: tc.timestampFields,
				CustomLayouts:   tc.customLayouts,
			}
			params := map[string]interface{}{
				"timestamp_fields": stageConfig.TimestampFields,
				"custom_layouts":   stageConfig.CustomLayouts,
			}

			stage, err := NewTimestampExtractionStage(params)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tsStage := stage.(*TimestampExtractionStage)

			logEntry := bufferpool.LogEntryPool.Acquire()
			defer bufferpool.LogEntryPool.Release(logEntry)
			logEntry.Fields = tc.inputEntryFields // Copy fields
			//logEntry.App = tc.name

			logEntry.Timestamp = tc.initialTimestamp

			// Act
			keep, processErr := tsStage.Process(logEntry)

			// Assert
			assert.True(t, keep, "Expected log to be kept")
			assert.NoError(t, processErr, "Expected no error from Process")

			if tc.expectTimestampToBeNow {
				// Allow a small delta for timestamps expected to be time.Now()
				assert.WithinDuration(t, time.Now(), logEntry.Timestamp, 1*time.Second,
					"Expected timestamp to be close to current time, got %v", logEntry.Timestamp)
			} else {
				assert.True(t, tc.expectedTimestamp.Equal(logEntry.Timestamp),
					"Expected timestamp %v, got %v", tc.expectedTimestamp, logEntry.Timestamp)
			}
		})
	}
}

func TestNewTimestampExtractionStage(t *testing.T) {
	t.Run("Valid config", func(t *testing.T) {
		params := map[string]interface{}{
			"timestamp_fields": []string{"time"},
			"custom_layouts":   []string{"Jan 02 15:04:05"},
		}
		stage, err := NewTimestampExtractionStage(params)
		require.NoError(t, err)
		assert.NotNil(t, stage)
		assert.Equal(t, "timestamp_extraction", stage.Name())

		tsStage := stage.(*TimestampExtractionStage)
		assert.Contains(t, tsStage.layouts, time.RFC3339Nano)  // Default layout
		assert.Contains(t, tsStage.layouts, "Jan 02 15:04:05") // Custom layout
		assert.Contains(t, tsStage.timestampFields, "time")
	})

	t.Run("Missing timestamp_fields", func(t *testing.T) {
		params := map[string]interface{}{
			"custom_layouts": []string{"Jan 02 15:04:05"},
		}
		stage, err := NewTimestampExtractionStage(params)
		require.NoError(t, err)
		assert.NotNil(t, stage)
	})

	t.Run("Empty timestamp_fields", func(t *testing.T) {
		params := map[string]interface{}{
			"timestamp_fields": []string{},
		}
		stage, err := NewTimestampExtractionStage(params)
		require.NoError(t, err)
		assert.NotNil(t, stage)
	})
}
