package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	StateFilePath          string
	LogBasePath            string
	LogFileExtensions      []string
	LogFilesIgnored        string
	Backend                string
	LokiURL                string
	EnrichedFileSuffix     string
	AppName                string
	AppIdentificationRegex string
	LogLevel               string
	Stages                 []StageConfig
}

// StageConfig holds the configuration for a single pipeline stage.
type StageConfig struct {
	Type      string
	AppliesTo string
	Params    map[string]interface{}
}

func Load() *Config {
	cfg := &Config{
		StateFilePath:          getEnv("STATE_FILE_PATH", "/cache/state.json"),
		LogBasePath:            getEnv("LOG_BASE_PATH", "/logs"),
		LogFilesIgnored:        getEnv("LOG_FILES_IGNORED", ""),
		LogFileExtensions:      getEnvSlice("LOG_FILE_EXTENSIONS", []string{".log"}),
		Backend:                getEnv("BACKEND", "file"),
		LokiURL:                getEnv("LOKI_URL", ""),
		EnrichedFileSuffix:     getEnv("ENRICHED_FILE_SUFFIX", ".enriched"),
		AppName:                getEnv("APP_NAME", ""),
		AppIdentificationRegex: getEnv("APP_IDENTIFICATION_REGEX", ""),
		LogLevel:               getEnv("LOG_LEVEL", "INFO"),
		Stages:                 loadStages(),
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

		// Check for AppliesTo specifically
		appliesToKey := fmt.Sprintf("STAGE_%d_APPLIES_TO", i)
		stage.AppliesTo = getEnv(appliesToKey, "")

		// Find all STAGE_i_* variables and add them to the stage's params.
		prefix := fmt.Sprintf("STAGE_%d_", i)
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, prefix) {
				parts := strings.SplitN(e, "=", 2)
				key := strings.ToLower(strings.TrimPrefix(parts[0], prefix))
				// Only add to params if it's not 'type' or 'applies_to'
				if key != "type" && key != "applies_to" {
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

func getEnvSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		return strings.Split(value, ",")
	}
	return defaultValue
}
