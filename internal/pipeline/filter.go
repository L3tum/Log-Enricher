package pipeline

import (
	"fmt"
	"regexp"

	"github.com/mitchellh/mapstructure"
)

// FilterStage drops or keeps logs based on configurable rules.
type FilterStage struct {
	config FilterStageConfig
	rules  []func(line []byte, entry map[string]interface{}) bool
}

// FilterStageConfig defines the rules for a filter stage.
type FilterStageConfig struct {
	Action    string `mapstructure:"action"`     // "keep" or "drop" on match.
	Match     string `mapstructure:"match"`      // "all" or "any" of the defined rules.
	Regex     string `mapstructure:"regex"`      // Regex pattern to match against the raw line.
	JSONField string `mapstructure:"json_field"` // Field to check in a JSON log.
	JSONValue string `mapstructure:"json_value"` // Regex to match against the JSON field's value.
	MinSize   int    `mapstructure:"min_size"`   // Minimum line size in characters.
	MaxSize   int    `mapstructure:"max_size"`   // Maximum line size in characters.
}

// NewFilterStage creates a new instance of a filter stage from its config.
func NewFilterStage(params map[string]interface{}) (*FilterStage, error) {
	var config FilterStageConfig
	if err := mapstructure.Decode(params, &config); err != nil {
		return nil, fmt.Errorf("failed to decode filter stage config: %w", err)
	}

	// Set defaults
	if config.Action == "" {
		config.Action = "drop" // Default to dropping a log if a rule matches.
	}
	if config.Match == "" {
		config.Match = "any" // Default to matching if any rule is met.
	}

	s := &FilterStage{config: config}
	rules := make([]func(line []byte, entry map[string]interface{}) bool, 0, 3)

	if config.Regex != "" {
		regex, err := regexp.Compile(config.Regex)
		if err != nil {
			return nil, fmt.Errorf("invalid regex for filter stage: %w", err)
		}
		rules = append(rules, func(line []byte, entry map[string]interface{}) bool {
			return regex.Match(line)
		})
	}

	if config.MinSize > 0 || config.MaxSize > 0 {
		minSize := config.MinSize
		maxSize := config.MaxSize
		rules = append(rules, func(line []byte, entry map[string]interface{}) bool {
			lineLen := len(line)
			if minSize > 0 && lineLen < minSize {
				return false
			}
			if maxSize > 0 && lineLen > maxSize {
				return false
			}
			return true
		})
	}

	if config.JSONField != "" && config.JSONValue != "" {
		jsonValRegex, err := regexp.Compile(config.JSONValue)
		if err != nil {
			return nil, fmt.Errorf("invalid json_value regex for filter stage: %w", err)
		}
		jsonField := config.JSONField
		rules = append(rules, func(line []byte, entry map[string]interface{}) bool {
			if entry == nil {
				return false
			}
			fieldVal, ok := entry[jsonField]
			if !ok {
				return false
			}
			if valStr, isString := fieldVal.(string); isString {
				return jsonValRegex.MatchString(valStr)
			}
			return false
		})
	}

	s.rules = rules
	return s, nil
}

func (s *FilterStage) Name() string {
	return "filter"
}

// Process applies the filter rules to a log entry.
func (s *FilterStage) Process(line []byte, entry map[string]interface{}) (bool, map[string]interface{}, error) {
	if len(s.rules) == 0 {
		return true, entry, nil // No rules defined, so we keep the log.
	}

	// Evaluate rules with short-circuiting.
	var finalMatch bool
	if s.config.Match == "any" {
		finalMatch = false
		for _, rule := range s.rules {
			if rule(line, entry) {
				finalMatch = true
				break
			}
		}
	} else { // "all"
		finalMatch = true
		for _, rule := range s.rules {
			if !rule(line, entry) {
				finalMatch = false
				break
			}
		}
	}

	// A rule match tells us whether the condition is met.
	// The action ("keep" or "drop") determines the outcome of that match.
	// This logic can be simplified to a single boolean expression.
	// If action is "keep", we keep if a match is found. `keep = finalMatch`
	// If action is "drop", we keep if NO match is found. `keep = !finalMatch`
	// This is equivalent to: (s.config.Action == "keep") == finalMatch
	shouldKeep := (s.config.Action == "keep") == finalMatch
	return shouldKeep, entry, nil
}
