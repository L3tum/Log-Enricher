package config

import (
	"reflect"
	"testing"
)

// Step 1: This test function, TestLoadConfig, verifies the loading of basic configuration values.
// It checks both default values and overrides from environment variables for various types.
func TestLoadConfig(t *testing.T) {
	t.Run("loads default values correctly", func(t *testing.T) {
		cfg := Load()

		if cfg.StateFilePath != "/cache/state.json" {
			t.Errorf("expected default StateFilePath to be '/cache/state.json', got %s", cfg.StateFilePath)
		}
		if cfg.LokiURL != "" {
			t.Errorf("expected default LokiURL to be empty, got %s", cfg.LokiURL)
		}
	})

	t.Run("overrides default values from environment variables", func(t *testing.T) {
		t.Setenv("CACHE_SIZE", "500")
		t.Setenv("STATE_FILE_PATH", "/test/state.json")
		t.Setenv("REQUERY_INTERVAL", "10s")
		t.Setenv("PLAINTEXT_PROCESSING_ENABLED", "false")
		t.Setenv("LOKI_URL", "http://loki:3100")
		t.Setenv("LOG_FILE_EXTENSIONS", ".log,.txt")
		t.Setenv("BACKEND", "loki")

		cfg := Load()

		if cfg.StateFilePath != "/test/state.json" {
			t.Errorf("expected overridden StateFilePath to be '/test/state.json', got %s", cfg.StateFilePath)
		}
		if cfg.LokiURL != "http://loki:3100" {
			t.Errorf("expected overridden LokiURL to be 'http://loki:3100', got %s", cfg.LokiURL)
		}

		expectedExtensions := []string{".log", ".txt"}
		if !reflect.DeepEqual(cfg.LogFileExtensions, expectedExtensions) {
			t.Errorf("expected overridden LogFileExtensions to be %v, got %v", expectedExtensions, cfg.LogFileExtensions)
		}

		if !reflect.DeepEqual(cfg.Backend, "loki") {
			t.Errorf("expected overridden Backends to be %v, got %v", "loki", cfg.Backend)
		}
	})
}

// Step 2: This test function, TestLoadStages, uses a table-driven approach to test the dynamic stage loading.
// It covers various scenarios to ensure the loadStages function in config.go behaves as expected.
func TestLoadStages(t *testing.T) {
	testCases := []struct {
		name           string
		envVars        map[string]string
		expectedStages []StageConfig
	}{
		{
			name:           "no stages defined",
			envVars:        map[string]string{},
			expectedStages: []StageConfig{},
		},
		{
			name: "single stage with no params",
			envVars: map[string]string{
				"STAGE_0_TYPE": "hostname",
			},
			expectedStages: []StageConfig{
				{Type: "hostname", Params: make(map[string]interface{})},
			},
		},
		{
			name: "single stage with params",
			envVars: map[string]string{
				"STAGE_0_TYPE": "geoip",
				"STAGE_0_DB":   "/path/to/db.mmdb",
			},
			expectedStages: []StageConfig{
				{Type: "geoip", Params: map[string]interface{}{"db": "/path/to/db.mmdb"}},
			},
		},
		{
			name: "multiple stages",
			envVars: map[string]string{
				"STAGE_0_TYPE": "hostname",
				"STAGE_1_TYPE": "geoip",
				"STAGE_1_DB":   "/path/to/db.mmdb",
			},
			expectedStages: []StageConfig{
				{Type: "hostname", Params: make(map[string]interface{})},
				{Type: "geoip", Params: map[string]interface{}{"db": "/path/to/db.mmdb"}},
			},
		},
		{
			name: "stops at missing stage number",
			envVars: map[string]string{
				"STAGE_0_TYPE": "hostname",
				"STAGE_2_TYPE": "geoip", // STAGE_1 is missing
			},
			expectedStages: []StageConfig{
				{Type: "hostname", Params: make(map[string]interface{})},
			},
		},
		{
			name: "param keys are lowercased",
			envVars: map[string]string{
				"STAGE_0_TYPE":      "custom",
				"STAGE_0_SomeParam": "value1",
				"STAGE_0_ANOTHER":   "value2",
			},
			expectedStages: []StageConfig{
				{Type: "custom", Params: map[string]interface{}{"someparam": "value1", "another": "value2"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variables for this test case
			for key, value := range tc.envVars {
				t.Setenv(key, value)
			}

			cfg := Load()

			if !reflect.DeepEqual(cfg.Stages, tc.expectedStages) {
				t.Errorf("expected stages %#v, but got %#v", tc.expectedStages, cfg.Stages)
			}
		})
	}
}
