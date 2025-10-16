package pipeline

import (
	"fmt"
	"log"
	"regexp"

	"github.com/mitchellh/mapstructure"
)

// ExtractedIPKey is the key used to store the found IP in the log entry map.
const ExtractedIPKey = "client_ip"

var ipRegex = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)

// ClientIpExtractionStageConfig defines the rules for finding the client IP.
type ClientIpExtractionStageConfig struct {
	// A list of fields to check in order for the client's IP address.
	ClientIPFields []string `mapstructure:"client_ip_fields"`
	RegexEnabled   bool     `mapstructure:"regex_enabled"`
}

// ClientIpExtractionStage is a pipeline stage that finds the client IP address
// in a log entry and adds it to a well-known field.
type ClientIpExtractionStage struct {
	config ClientIpExtractionStageConfig
}

// NewClientIpExtractionStage creates a new IP extraction stage.
func NewClientIpExtractionStage(params map[string]interface{}) (Stage, error) {
	var config ClientIpExtractionStageConfig
	if err := mapstructure.Decode(params, &config); err != nil {
		return nil, fmt.Errorf("failed to decode client ip extraction stage config: %w", err)
	}
	if len(config.ClientIPFields) == 0 {
		return nil, fmt.Errorf("client_ip_fields must be configured for the client_ip_extraction stage")
	}

	log.Printf("Client IP extraction stage enabled (fields: %v)", config.ClientIPFields)
	return &ClientIpExtractionStage{config: config}, nil
}

func (s *ClientIpExtractionStage) Name() string {
	return "client_ip_extraction"
}

// Process finds the IP and adds it to the entry.
func (s *ClientIpExtractionStage) Process(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
	if entry == nil {
		return true, entry, nil // Nothing to process.
	}

	for _, field := range s.config.ClientIPFields {
		if val, ok := entry[field]; ok {
			if ip, isString := val.(string); isString && ip != "" {
				entry[ExtractedIPKey] = ip
				// Found it, no need to check other fields.
				// Delete all other fields though
				deleteFields(entry, s.config.ClientIPFields)

				return true, entry, nil
			}
		}
	}

	if s.config.RegexEnabled {
		if msgVal, ok := entry["msg"]; ok {
			if msgStr, isString := msgVal.(string); isString {
				ip := ipRegex.FindString(msgStr)
				if ip != "" {
					entry[ExtractedIPKey] = ip
					return true, entry, nil
				}
			}
		} else {
			// Also try to find the IP in the log line itself, but only if there is no msg field
			if ip := ipRegex.Find(line); ip != nil {
				entry[ExtractedIPKey] = string(ip)
				return true, entry, nil
			}
		}
	}

	return true, entry, nil // Keep log even if IP not found.
}

func deleteFields(entry map[string]interface{}, fields []string) {
	for _, field := range fields {
		if field != ExtractedIPKey {
			delete(entry, field)
		}
	}
}
