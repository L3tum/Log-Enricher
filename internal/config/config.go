package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	CacheSize                  int
	StateFilePath              string
	RequeryInterval            time.Duration
	LogBasePath                string
	LogFileExtensions          []string
	PlaintextProcessingEnabled bool
	Backends                   []string
	LokiURL                    string
	EnrichedFileSuffix         string
	Stages                     []StageConfig
}

// StageConfig holds the configuration for a single pipeline stage.
type StageConfig struct {
	Type   string
	Params map[string]interface{}
}

func Load() *Config {
	cfg := &Config{
		CacheSize:                  getEnvInt("CACHE_SIZE", 10000),
		StateFilePath:              getEnv("STATE_FILE_PATH", "/cache/state.json"),
		RequeryInterval:            getEnvDuration("REQUERY_INTERVAL", 5*time.Minute),
		LogBasePath:                getEnv("LOG_BASE_PATH", "/logs"),
		PlaintextProcessingEnabled: getEnvBool("PLAINTEXT_PROCESSING_ENABLED", true),
		LogFileExtensions:          getEnvSlice("LOG_FILE_EXTENSIONS", []string{".log"}),
		Backends:                   getEnvSlice("BACKENDS", []string{"file"}),
		LokiURL:                    getEnv("LOKI_URL", ""),
		EnrichedFileSuffix:         getEnv("ENRICHED_FILE_SUFFIX", ".enriched"),
		Stages:                     loadStages(),
	}

	return cfg
}

// loadStages dynamically loads pipeline stage configurations from environment variables.
func loadStages() []StageConfig {
	stages := []StageConfig{}
	for i := 0; ; i++ {
		stageTypeKey := fmt.Sprintf("STAGE_%d_TYPE", i)
		stageType := getEnv(stageTypeKey, "")
		if stageType == "" {
			break // No more stages defined.
		}

		stage := StageConfig{
			Type:   stageType,
			Params: make(map[string]interface{}),
		}

		// Find all STAGE_i_* variables and add them to the stage's params.
		prefix := fmt.Sprintf("STAGE_%d_", i)
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, prefix) {
				parts := strings.SplitN(e, "=", 2)
				key := strings.ToLower(strings.TrimPrefix(parts[0], prefix))
				if key != "type" {
					stage.Params[key] = parts[1]
				}
			}
		}
		stages = append(stages, stage)
	}
	return stages
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		return strings.Split(value, ",")
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}
