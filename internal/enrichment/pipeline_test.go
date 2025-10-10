package enrichment

import (
	"io"
	"log"
	"reflect"
	"testing"

	"log-enricher/internal/models"
)

// mockStage is a simple implementation of the Stage interface for testing.
type mockStage struct {
	name string
}

func (m *mockStage) Run(ip string, result *models.Result) (updated bool) {
	// No-op for testing pipeline construction
	return false
}

func (m *mockStage) Name() string {
	return m.name
}

func TestNewPipeline(t *testing.T) {
	testCases := []struct {
		name               string
		inputStages        []Stage
		expectedStageCount int
		expectedStageNames []string
	}{
		{
			name:               "Two valid stages",
			inputStages:        []Stage{&mockStage{name: "A"}, &mockStage{name: "B"}},
			expectedStageCount: 2,
			expectedStageNames: []string{"A", "B"},
		},
		{
			name:               "One valid, one nil",
			inputStages:        []Stage{&mockStage{name: "A"}, nil},
			expectedStageCount: 1,
			expectedStageNames: []string{"A"},
		},
		{
			name:               "One nil, one valid",
			inputStages:        []Stage{nil, &mockStage{name: "B"}},
			expectedStageCount: 1,
			expectedStageNames: []string{"B"},
		},
		{
			name:               "All nil stages",
			inputStages:        []Stage{nil, nil, nil},
			expectedStageCount: 0,
			expectedStageNames: []string{},
		},
		{
			name:               "No stages provided",
			inputStages:        []Stage{},
			expectedStageCount: 0,
			expectedStageNames: []string{},
		},
	}

	// The NewPipeline function logs, so discard its output for clean test results.
	originalOutput := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(originalOutput)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The function being tested
			pipeline := NewPipeline(tc.inputStages...)

			if len(pipeline.stages) != tc.expectedStageCount {
				t.Errorf("expected %d stages, but got %d", tc.expectedStageCount, len(pipeline.stages))
			}

			actualNames := make([]string, 0, len(pipeline.stages))
			for _, s := range pipeline.stages {
				actualNames = append(actualNames, s.Name())
			}

			if !reflect.DeepEqual(actualNames, tc.expectedStageNames) {
				t.Errorf("expected stage names %v, but got %v", tc.expectedStageNames, actualNames)
			}
		})
	}
}
