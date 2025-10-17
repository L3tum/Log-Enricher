package pipeline

import (
	"errors"
	"log-enricher/internal/bufferpool"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStage is a configurable mock for the Stage interface.
type mockStage struct {
	name       string
	process    func(line []byte, entry *bufferpool.LogEntry) (bool, error)
	callCount  int
	lastInput  map[string]interface{} // Stores a copy of the Fields map
	isEnricher bool
}

func (m *mockStage) Name() string {
	return m.name
}

// Process now accepts *bufferpool.LogEntry
func (m *mockStage) Process(line []byte, entry *bufferpool.LogEntry) (bool, error) {
	m.callCount++
	// Make a copy of the Fields map to avoid issues with pooling.
	m.lastInput = make(map[string]interface{}, len(entry.Fields))
	for k, v := range entry.Fields {
		m.lastInput[k] = v
	}
	return m.process(line, entry) // Call the mock's process function
}

func TestManager_Process(t *testing.T) {
	t.Run("Single stage modifies entry", func(t *testing.T) {
		stage1 := &mockStage{
			name: "modifier",
			process: func(line []byte, entry *bufferpool.LogEntry) (bool, error) {
				entry.Fields["modified"] = true
				return true, nil
			},
		}
		m := &manager{stages: []Stage{stage1}}
		inputEntry := bufferpool.GetLogEntry()
		inputEntry.Fields["original"] = true

		keep := m.Process(nil, &inputEntry) // Pass pointer to LogEntry

		assert.True(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, map[string]interface{}{"original": true, "modified": true}, inputEntry.Fields) // Assert on Fields
		bufferpool.PutLogEntry(inputEntry)
	})

	t.Run("Multi-stage pipeline passes entry through", func(t *testing.T) {
		stage1 := &mockStage{
			name: "stage1",
			process: func(line []byte, entry *bufferpool.LogEntry) (bool, error) {
				entry.Fields["stage1_ran"] = true
				return true, nil
			},
		}
		stage2 := &mockStage{
			name: "stage2",
			process: func(line []byte, entry *bufferpool.LogEntry) (bool, error) {
				entry.Fields["stage2_ran"] = true
				return true, nil
			},
		}
		m := &manager{stages: []Stage{stage1, stage2}}
		inputEntry := bufferpool.GetLogEntry()

		keep := m.Process(nil, &inputEntry) // Pass pointer to LogEntry

		assert.True(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 1, stage2.callCount)
		assert.Equal(t, map[string]interface{}{"stage1_ran": true}, stage2.lastInput) // Assert on Fields
		assert.Equal(t, map[string]interface{}{"stage1_ran": true, "stage2_ran": true}, inputEntry.Fields)
		bufferpool.PutLogEntry(inputEntry)
	})

	t.Run("Stage drops log and stops processing", func(t *testing.T) {
		stage1 := &mockStage{
			name: "dropper",
			process: func(line []byte, entry *bufferpool.LogEntry) (bool, error) {
				return false, nil // Drop the log
			},
		}
		stage2 := &mockStage{name: "never_called"}
		m := &manager{stages: []Stage{stage1, stage2}}
		inputEntry := bufferpool.GetLogEntry()

		keep := m.Process(nil, &inputEntry) // Pass pointer to LogEntry

		assert.False(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 0, stage2.callCount, "second stage should not have been called")
		bufferpool.PutLogEntry(inputEntry)
	})

	t.Run("Stage returns error and stops processing", func(t *testing.T) {
		stage1 := &mockStage{
			name: "error_producer",
			process: func(line []byte, entry *bufferpool.LogEntry) (bool, error) {
				return false, errors.New("something went wrong")
			},
		}
		stage2 := &mockStage{name: "never_called"}
		m := &manager{stages: []Stage{stage1, stage2}}
		inputEntry := bufferpool.GetLogEntry()

		keep := m.Process(nil, &inputEntry) // Pass pointer to LogEntry

		assert.False(t, keep, "log should be dropped on error")
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 0, stage2.callCount, "second stage should not have been called")
		bufferpool.PutLogEntry(inputEntry)
	})
}

func TestManager_EnrichmentStages(t *testing.T) {
	// We need concrete types to test this, as it relies on type assertion.
	enrichStage, _ := NewEnrichmentStage(nil)
	filterStage, _ := NewFilterStage(nil)

	m := &manager{
		stages: []Stage{filterStage, enrichStage},
	}

	enrichers := m.EnrichmentStages()
	require.Len(t, enrichers, 1)
	assert.Equal(t, "enrichment", enrichers[0].Name())
}
