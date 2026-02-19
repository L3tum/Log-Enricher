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
		if cfg.PromtailHTTPEnabled {
			t.Errorf("expected default PromtailHTTPEnabled to be false")
		}
		if cfg.PromtailHTTPAddr != "127.0.0.1:3500" {
			t.Errorf("expected default PromtailHTTPAddr to be '127.0.0.1:3500', got %s", cfg.PromtailHTTPAddr)
		}
		if cfg.PromtailHTTPMaxBodyBytes != 10*1024*1024 {
			t.Errorf("expected default PromtailHTTPMaxBodyBytes to be 10485760, got %d", cfg.PromtailHTTPMaxBodyBytes)
		}
		if cfg.PromtailHTTPBearerToken != "" {
			t.Errorf("expected default PromtailHTTPBearerToken to be empty")
		}
		if cfg.PromtailHTTPSourceRoot != "/cache/promtail" {
			t.Errorf("expected default PromtailHTTPSourceRoot to be '/cache/promtail', got %s", cfg.PromtailHTTPSourceRoot)
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
		t.Setenv("PROMTAIL_HTTP_ENABLED", "true")
		t.Setenv("PROMTAIL_HTTP_ADDR", "0.0.0.0:8080")
		t.Setenv("PROMTAIL_HTTP_MAX_BODY_BYTES", "2048")
		t.Setenv("PROMTAIL_HTTP_BEARER_TOKEN", "secret-token")
		t.Setenv("PROMTAIL_HTTP_SOURCE_ROOT", "/tmp/promtail")

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
		if !cfg.PromtailHTTPEnabled {
			t.Errorf("expected PromtailHTTPEnabled to be true")
		}
		if cfg.PromtailHTTPAddr != "0.0.0.0:8080" {
			t.Errorf("expected PromtailHTTPAddr to be '0.0.0.0:8080', got %s", cfg.PromtailHTTPAddr)
		}
		if cfg.PromtailHTTPMaxBodyBytes != 2048 {
			t.Errorf("expected PromtailHTTPMaxBodyBytes to be 2048, got %d", cfg.PromtailHTTPMaxBodyBytes)
		}
		if cfg.PromtailHTTPBearerToken != "secret-token" {
			t.Errorf("expected PromtailHTTPBearerToken to be 'secret-token', got %s", cfg.PromtailHTTPBearerToken)
		}
		if cfg.PromtailHTTPSourceRoot != "/tmp/promtail" {
			t.Errorf("expected PromtailHTTPSourceRoot to be '/tmp/promtail', got %s", cfg.PromtailHTTPSourceRoot)
		}
	})

	t.Run("falls back to defaults for invalid bool and int values", func(t *testing.T) {
		t.Setenv("PROMTAIL_HTTP_ENABLED", "not-a-bool")
		t.Setenv("PROMTAIL_HTTP_MAX_BODY_BYTES", "not-an-int")

		cfg := Load()

		if cfg.PromtailHTTPEnabled {
			t.Errorf("expected invalid PROMTAIL_HTTP_ENABLED to fall back to false")
		}
		if cfg.PromtailHTTPMaxBodyBytes != 10*1024*1024 {
			t.Errorf("expected invalid PROMTAIL_HTTP_MAX_BODY_BYTES to fall back to 10485760, got %d", cfg.PromtailHTTPMaxBodyBytes)
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
			name: "applies_to is not included in params",
			envVars: map[string]string{
				"STAGE_0_TYPE":       "hostname_enrichment",
				"STAGE_0_APPLIES_TO": `\\.log$`,
			},
			expectedStages: []StageConfig{
				{Type: "hostname_enrichment", AppliesTo: `\\.log$`, Params: make(map[string]interface{})},
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
