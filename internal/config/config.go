package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DNSServer           string
	CacheSize           int
	StateFilePath       string
	RequeryInterval     time.Duration
	LogBasePath         string
	LogFileExtensions   []string
	ClientIPFields      []string
	GeoIPDatabasePath   string
	Backends            []string
	LokiURL             string
	EnrichedFileSuffix  string
	EnableHostnameStage bool
	EnableGeoIpStage    bool
	EnableCrowdsecStage bool
	EnableRDNS          bool
	EnableMDNS          bool
	EnableLLMNR         bool
	EnableNetBIOS       bool
	CrowdsecLapiURL     string
	CrowdsecLapiKey     string
}

func Load() *Config {
	cfg := &Config{
		DNSServer:           getEnv("DNS_SERVER", "8.8.8.8:53"),
		CacheSize:           getEnvInt("CACHE_SIZE", 10000),
		StateFilePath:       getEnv("STATE_FILE_PATH", "/cache/state.json"),
		RequeryInterval:     getEnvDuration("REQUERY_INTERVAL", 5*time.Minute),
		LogBasePath:         getEnv("LOG_BASE_PATH", "/logs"),
		LogFileExtensions:   getEnvSlice("LOG_FILE_EXTENSIONS", []string{".log"}),
		ClientIPFields:      getEnvSlice("CLIENT_IP_FIELDS", []string{"client_ip", "remote_addr"}),
		GeoIPDatabasePath:   getEnv("GEOIP_DATABASE_PATH", ""),
		Backends:            getEnvSlice("BACKENDS", []string{"file"}),
		LokiURL:             getEnv("LOKI_URL", ""),
		EnrichedFileSuffix:  getEnv("ENRICHED_FILE_SUFFIX", ".enriched"),
		EnableHostnameStage: getEnvBool("ENABLE_HOSTNAME_STAGE", true),
		EnableGeoIpStage:    getEnvBool("ENABLE_GEOIP_STAGE", false),
		EnableCrowdsecStage: getEnvBool("ENABLE_CROWDSEC_STAGE", false),
		EnableRDNS:          getEnvBool("ENABLE_RDNS", true),
		EnableMDNS:          getEnvBool("ENABLE_MDNS", true),
		EnableLLMNR:         getEnvBool("ENABLE_LLMNR", true),
		EnableNetBIOS:       getEnvBool("ENABLE_NETBIOS", true),
		CrowdsecLapiURL:     getEnv("CROWDSEC_LAPI_URL", ""),
		CrowdsecLapiKey:     getEnv("CROWDSEC_LAPI_KEY", ""),
	}

	return cfg
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
