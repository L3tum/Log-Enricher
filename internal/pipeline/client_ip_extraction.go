package pipeline

import (
	"fmt"
	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log/slog"
	"net"
	"strings"

	"github.com/mitchellh/mapstructure"
)

// ClientIpExtractionStageConfig defines the rules for finding the client IP.
type ClientIpExtractionStageConfig struct {
	ClientIPFields []string `mapstructure:"client_ip_fields"`
	TargetField    string   `mapstructure:"target_field"`
}

type clientIpField struct {
	name             string
	needsToBeCleaned bool
}

// ClientIpExtractionStage is a pipeline stage that finds the client IP address
// in a log entry and adds it to a well-known field.
type ClientIpExtractionStage struct {
	config                  ClientIpExtractionStageConfig
	mostLikelyClientIpField *cache.PersistedCache[clientIpField]
}

// NewClientIpExtractionStage creates a new IP extraction stage.
func NewClientIpExtractionStage(params map[string]interface{}) (Stage, error) {
	var config ClientIpExtractionStageConfig

	// Configure mapstructure to handle comma-separated strings as slices.
	decoderConfig := &mapstructure.DecoderConfig{
		Result: &config,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToSliceHookFunc(","), // This hook splits strings by comma into slices
			mapstructure.TextUnmarshallerHookFunc(),
		),
	}

	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapstructure decoder: %w", err)
	}

	if err := decoder.Decode(params); err != nil {
		return nil, fmt.Errorf("failed to decode client ip extraction stage config: %w", err)
	}

	// Trim spaces from each field name after decoding
	for i, field := range config.ClientIPFields {
		config.ClientIPFields[i] = strings.TrimSpace(field)
	}

	if len(config.ClientIPFields) == 0 {
		return nil, fmt.Errorf("client_ip_fields must be configured for the client_ip_extraction stage")
	}

	if config.TargetField == "" {
		config.TargetField = "client_ip"
	}

	slog.Info("Client IP extraction stage enabled", "fields", config.ClientIPFields)
	return &ClientIpExtractionStage{
		config:                  config,
		mostLikelyClientIpField: cache.NewPersistedCache[clientIpField]("client_ip", 10, 100, false),
	}, nil
}

func (s *ClientIpExtractionStage) Name() string {
	return "client_ip_extraction"
}

// Process finds the IP and adds it to the entry.
func (s *ClientIpExtractionStage) Process(entry *models.LogEntry) (bool, error) {
	// Add the length of the field map to the cache key to invalidate a negative cache if the client_ip may appear in only some messages
	cacheKey := entry.SourcePath + string(rune(len(entry.Fields)))

	if field, ok := s.mostLikelyClientIpField.Get(cacheKey); ok {
		// If the name is empty, no field was found before and we skip this line
		if field.name == "" {
			s.mostLikelyClientIpField.Hit()
			return true, nil
		}

		if ip := s.validateAndCleanIP(&field, entry); ip != "" {
			s.mostLikelyClientIpField.Hit()
			entry.Fields[s.config.TargetField] = ip
			slog.Debug("ClientIpExtractionStage: Found IP in most likely field", "field", field, "ip", ip)
			s.deleteFields(entry, s.config.ClientIPFields)
			return true, nil
		}
	}

	s.mostLikelyClientIpField.Miss()
	clientField := clientIpField{needsToBeCleaned: true}

	// Try to find IP in configured fields
	for _, field := range s.config.ClientIPFields {
		clientField.name = field

		if ip := s.validateAndCleanIP(&clientField, entry); ip != "" {
			s.mostLikelyClientIpField.Set(cacheKey, clientField)

			entry.Fields[s.config.TargetField] = ip
			slog.Debug("ClientIpExtractionStage: Found IP in field", "field", field, "ip", ip)
			s.deleteFields(entry, s.config.ClientIPFields)
			return true, nil
		}
	}

	// If we reach this point, the IP was not found in any of the configured fields
	clientField.name = ""
	s.mostLikelyClientIpField.Set(cacheKey, clientField)

	slog.Debug("ClientIpExtractionStage: No client IP found after all attempts.")
	return true, nil // Keep log even if IP not found.
}

// validateAndCleanIP checks if the input is a valid IP address and removes any port number.
func (s *ClientIpExtractionStage) validateAndCleanIP(field *clientIpField, logEntry *models.LogEntry) string {
	var val interface{}
	var ok bool

	if val, ok = logEntry.Fields[field.name]; !ok {
		return ""
	}

	var ip string
	if ip, ok = val.(string); !ok {
		return ""
	}

	if field.needsToBeCleaned {
		// Remove port if present
		host, _, err := net.SplitHostPort(ip)
		if err != nil {
			// If SplitHostPort fails, use the original string
			host = ip
		}

		// Try parsing as IP
		parsedIP := net.ParseIP(strings.TrimSpace(host))
		if parsedIP == nil {
			return ""
		}

		parsedIpString := parsedIP.String()

		if parsedIpString == ip {
			field.needsToBeCleaned = false
		}

		return parsedIpString
	}

	return ip
}

func (s *ClientIpExtractionStage) deleteFields(entry *models.LogEntry, fields []string) {
	for _, field := range fields {
		if field != s.config.TargetField {
			delete(entry.Fields, field)
			slog.Debug("ClientIpExtractionStage: Deleted field", "field", field)
		}
	}
}
