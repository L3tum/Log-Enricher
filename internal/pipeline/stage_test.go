package pipeline

import (
	"errors"
	"log-enricher/internal/bufferpool"
	"log-enricher/internal/models"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockStage is a configurable mock for the Stage interface.
type mockStage struct {
	name      string
	process   func(entry *models.LogEntry) (bool, error)
	callCount int
	lastInput map[string]interface{} // Stores a copy of the Fields map
}

func (m *mockStage) Name() string {
	return m.name
}

func (m *mockStage) Process(entry *models.LogEntry) (bool, error) {
	m.callCount++
	// Make a copy of the Fields map to avoid issues with pooling.
	// Ensure entry.Fields is not nil before accessing its length or iterating.
	if entry.Fields == nil {
		entry.Fields = make(map[string]interface{})
	}
	m.lastInput = make(map[string]interface{}, len(entry.Fields))
	for k, v := range entry.Fields {
		m.lastInput[k] = v
	}
	if m.process != nil {
		return m.process(entry) // Call the mock's process function
	}
	return true, nil
}

func TestProcessPipeline_Process(t *testing.T) {
	t.Run("Single stage modifies entry", func(t *testing.T) {
		stage1 := &mockStage{
			name: "modifier",
			process: func(entry *models.LogEntry) (bool, error) {
				entry.Fields["modified"] = true
				return true, nil
			},
		}
		m := &manager{stages: []appliedToStage{{stage: stage1}}}
		inputEntry := bufferpool.LogEntryPool.Acquire()
		defer bufferpool.LogEntryPool.Release(inputEntry)
		inputEntry.Fields["original"] = true

		pipeline := m.GetProcessPipeline("")
		keep := pipeline.Process(inputEntry)

		assert.True(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, map[string]interface{}{"original": true, "modified": true}, inputEntry.Fields) // Assert on Fields
	})

	t.Run("Multi-stage pipeline passes entry through", func(t *testing.T) {
		stage1 := &mockStage{
			name: "stage1",
			process: func(entry *models.LogEntry) (bool, error) {
				entry.Fields["stage1_ran"] = true
				return true, nil
			},
		}
		stage2 := &mockStage{
			name: "stage2",
			process: func(entry *models.LogEntry) (bool, error) {
				// Ensure entry.Fields is initialized before modification
				if entry.Fields == nil {
					entry.Fields = make(map[string]interface{})
				}
				entry.Fields["stage2_ran"] = true
				return true, nil
			},
		}
		m := &manager{stages: []appliedToStage{{stage: stage1}, {stage: stage2}}}
		inputEntry := bufferpool.LogEntryPool.Acquire()
		defer bufferpool.LogEntryPool.Release(inputEntry)
		// Initialize Fields map if GetLogEntry doesn't guarantee it
		if inputEntry.Fields == nil {
			inputEntry.Fields = make(map[string]interface{})
		}

		pipeline := m.GetProcessPipeline("")
		keep := pipeline.Process(inputEntry)

		assert.True(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 1, stage2.callCount)
		assert.Equal(t, map[string]interface{}{"stage1_ran": true}, stage2.lastInput) // Assert on Fields
		assert.Equal(t, map[string]interface{}{"stage1_ran": true, "stage2_ran": true}, inputEntry.Fields)
	})

	t.Run("Stage drops log and stops processing", func(t *testing.T) {
		stage1 := &mockStage{
			name: "dropper",
			process: func(entry *models.LogEntry) (bool, error) {
				return false, nil // Drop the log
			},
		}
		stage2 := &mockStage{name: "never_called"}
		m := &manager{stages: []appliedToStage{{stage: stage1}, {stage: stage2}}}
		inputEntry := bufferpool.LogEntryPool.Acquire()
		defer bufferpool.LogEntryPool.Release(inputEntry)
		// Initialize Fields map if GetLogEntry doesn't guarantee it
		if inputEntry.Fields == nil {
			inputEntry.Fields = make(map[string]interface{})
		}

		pipeline := m.GetProcessPipeline("")
		keep := pipeline.Process(inputEntry)

		assert.False(t, keep)
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 0, stage2.callCount, "second stage should not have been called")
	})

	t.Run("Stage returns error and stops processing", func(t *testing.T) {
		stage1 := &mockStage{
			name: "error_producer",
			process: func(entry *models.LogEntry) (bool, error) {
				return false, errors.New("something went wrong")
			},
		}
		stage2 := &mockStage{name: "never_called"}
		m := &manager{stages: []appliedToStage{{stage: stage1}, {stage: stage2}}}
		inputEntry := bufferpool.LogEntryPool.Acquire()
		defer bufferpool.LogEntryPool.Release(inputEntry)
		// Initialize Fields map if GetLogEntry doesn't guarantee it
		if inputEntry.Fields == nil {
			inputEntry.Fields = make(map[string]interface{})
		}

		pipeline := m.GetProcessPipeline("")
		keep := pipeline.Process(inputEntry)

		assert.False(t, keep, "log should be dropped on error")
		assert.Equal(t, 1, stage1.callCount)
		assert.Equal(t, 0, stage2.callCount, "second stage should not have been called")
	})
}

func TestManager_GetProcessPipeline(t *testing.T) {
	t.Run("Correct stages are selected based on file path", func(t *testing.T) {
		// Stage that applies to .log files
		stage1 := &mockStage{name: "log_stage"}
		appliesToLog, _ := regexp.Compile(`\.log$`)

		// Stage that applies to .txt files
		stage2 := &mockStage{name: "txt_stage"}
		appliesToTxt, _ := regexp.Compile(`\.txt$`)

		// Stage that applies to everything (nil regex)
		stage3 := &mockStage{name: "always_run"}

		m := &manager{
			stages: []appliedToStage{
				{stage: stage1, appliesTo: appliesToLog},
				{stage: stage2, appliesTo: appliesToTxt},
				{stage: stage3, appliesTo: nil},
			},
		}

		// Test .log file
		pipelineForLog := m.GetProcessPipeline("some/file.log").(*processPipeline)
		assert.Len(t, pipelineForLog.stages, 2)
		assert.Equal(t, "log_stage", pipelineForLog.stages[0].Name())
		assert.Equal(t, "always_run", pipelineForLog.stages[1].Name())

		// Test .txt file
		pipelineForTxt := m.GetProcessPipeline("another/file.txt").(*processPipeline)
		assert.Len(t, pipelineForTxt.stages, 2)
		assert.Equal(t, "txt_stage", pipelineForTxt.stages[0].Name())
		assert.Equal(t, "always_run", pipelineForTxt.stages[1].Name())

		// Test other file type
		pipelineForOther := m.GetProcessPipeline("archive.zip").(*processPipeline)
		assert.Len(t, pipelineForOther.stages, 1)
		assert.Equal(t, "always_run", pipelineForOther.stages[0].Name())
	})

	t.Run("Pipeline processes only with matching stages", func(t *testing.T) {
		stage1 := &mockStage{name: "log_stage"}
		appliesToLog, _ := regexp.Compile(`\.log$`)

		stage2 := &mockStage{name: "txt_stage"}
		appliesToTxt, _ := regexp.Compile(`\.txt$`)

		stage3 := &mockStage{name: "always_run"}
		m := &manager{
			stages: []appliedToStage{
				{stage: stage1, appliesTo: appliesToLog},
				{stage: stage2, appliesTo: appliesToTxt},
				{stage: stage3, appliesTo: nil},
			},
		}

		inputEntry := bufferpool.LogEntryPool.Acquire()
		defer bufferpool.LogEntryPool.Release(inputEntry)

		// Get pipeline for a .log file and process
		pipeline := m.GetProcessPipeline("test.log")
		pipeline.Process(inputEntry)

		// Check which stages were called
		assert.Equal(t, 1, stage1.callCount) // log_stage should run
		assert.Equal(t, 0, stage2.callCount) // txt_stage should NOT run
		assert.Equal(t, 1, stage3.callCount) // always_run should run
	})
}
