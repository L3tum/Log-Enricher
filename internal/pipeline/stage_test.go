package pipeline

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStage is a configurable mock for the Stage interface.
type mockStage struct {
	name       string
	process    func(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error)
	callCount  int
	lastInput  map[string]interface{}
	isEnricher bool
}

func (m *mockStage) Name() string {
	return m.name
}

func (m *mockStage) Process(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
	m.callCount++
	// Make a copy of the map to avoid issues with pooling.
	m.lastInput = make(map[string]interface{}, len(entry))
	for k, v := range entry {
		m.lastInput[k] = v
	}
	return m.process(line, entry)
}

func TestManager_Process(t *testing.T) {
	t.Run("Single stage modifies entry", func(t *testing.T) {
		stage1 := &mockStage{
			name: "modifier",
			process: func(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
				entry["modified"] = true
				return true, entry, nil
			},
		}
		m := &manager{stages: []Stage{stage1}}
		input := map[string]interface{}{"original": true}

		keep, finalEntry := m.Process(nil, input)

		assert.True(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, map[string]interface{}{"original": true, "modified": true}, finalEntry)
	})

	t.Run("Multi-stage pipeline passes entry through", func(t *testing.T) {
		stage1 := &mockStage{
			name: "stage1",
			process: func(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
				entry["stage1_ran"] = true
				return true, entry, nil
			},
		}
		stage2 := &mockStage{
			name: "stage2",
			process: func(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
				entry["stage2_ran"] = true
				return true, entry, nil
			},
		}
		m := &manager{stages: []Stage{stage1, stage2}}
		input := map[string]interface{}{}

		keep, finalEntry := m.Process(nil, input)

		assert.True(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 1, stage2.callCount)
		assert.Equal(t, map[string]interface{}{"stage1_ran": true}, stage2.lastInput)
		assert.Equal(t, map[string]interface{}{"stage1_ran": true, "stage2_ran": true}, finalEntry)
	})

	t.Run("Stage drops log and stops processing", func(t *testing.T) {
		stage1 := &mockStage{
			name: "dropper",
			process: func(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
				return false, entry, nil // Drop the log
			},
		}
		stage2 := &mockStage{name: "never_called"}
		m := &manager{stages: []Stage{stage1, stage2}}

		keep, _ := m.Process(nil, map[string]interface{}{})

		assert.False(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 0, stage2.callCount, "second stage should not have been called")
	})

	t.Run("Stage returns error and stops processing", func(t *testing.T) {
		stage1 := &mockStage{
			name: "error_producer",
			process: func(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
				return false, nil, errors.New("something went wrong")
			},
		}
		stage2 := &mockStage{name: "never_called"}
		m := &manager{stages: []Stage{stage1, stage2}}

		keep, finalEntry := m.Process(nil, map[string]interface{}{})

		assert.False(t, keep, "log should be dropped on error")
		assert.Nil(t, finalEntry)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 0, stage2.callCount, "second stage should not have been called")
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
