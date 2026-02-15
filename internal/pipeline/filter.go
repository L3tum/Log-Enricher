package pipeline

import (
	"fmt"
	"log-enricher/internal/models"
	"regexp"
	"time"

	"github.com/mitchellh/mapstructure"
)

// FilterRule defines the interface for a single filtering condition.
// A rule's Matches method should return true if the log entry satisfies the condition
// that the filter stage is configured to act upon (e.g., if it's an "error" log,
// or if it's "too old", or "too large").
type FilterRule interface {
	Matches(entry *models.LogEntry) bool
}

// RegexRule implements FilterRule for regex matching against the raw log line.
type RegexRule struct {
	regex *regexp.Regexp
}

// Matches returns true if the line matches the configured regex.
func (r *RegexRule) Matches(entry *models.LogEntry) bool {
	return r.regex.Match(entry.LogLine)
}

// SizeRule implements FilterRule for minimum and maximum line size checks.
type SizeRule struct {
	minSize int
	maxSize int
}

// Matches returns true if the line's length violates the configured min/max size.
// This means it returns true if the line is too short OR too long.
func (r *SizeRule) Matches(entry *models.LogEntry) bool {
	lineLen := len(entry.LogLine)
	if r.minSize > 0 && lineLen < r.minSize {
		return true // Line is too short, matches the condition for filtering
	}
	if r.maxSize > 0 && lineLen > r.maxSize {
		return true // Line is too long, matches the condition for filtering
	}
	return false // Line is within size limits, does not match the condition for filtering
}

// MaxAgeRule implements FilterRule for checking if a log entry is older than a specified duration.
type MaxAgeRule struct {
	duration time.Duration
}

// Matches returns true if the log entry's timestamp is older than the current time minus the MaxAge duration.
func (r *MaxAgeRule) Matches(entry *models.LogEntry) bool {
	// Returns true if the entry's timestamp is BEFORE (older than) the cutoff time.
	return entry.Timestamp.Before(time.Now().Add(-r.duration))
}

// JSONFieldRule implements FilterRule for matching a regex against a specific JSON field's value.
type JSONFieldRule struct {
	jsonField    string
	jsonValRegex *regexp.Regexp
}

// Matches returns true if the specified JSON field exists and its string value matches the configured regex.
func (r *JSONFieldRule) Matches(entry *models.LogEntry) bool {
	fieldVal, ok := entry.Fields[r.jsonField]
	if !ok {
		return false
	}
	if valStr, isString := fieldVal.(string); isString {
		return r.jsonValRegex.MatchString(valStr)
	}
	return false
}

// FilterStage drops or keeps logs based on configurable rules.
type FilterStage struct {
	config FilterStageConfig
	rules  []FilterRule // Now holds an interface slice
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
	MaxAge    int    `mapstructure:"max_age"`    // Maximum age of a log in seconds.
}

// NewFilterStage creates a new instance of a filter stage from its config.
// It initializes individual FilterRule implementations based on the configuration.
func NewFilterStage(params map[string]interface{}) (*FilterStage, error) {
	var config FilterStageConfig
	if err := mapstructure.WeakDecode(params, &config); err != nil {
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
	rules := make([]FilterRule, 0, 4) // Pre-allocate for potential rules

	if config.Regex != "" {
		regex, err := regexp.Compile(config.Regex)
		if err != nil {
			return nil, fmt.Errorf("invalid regex for filter stage: %w", err)
		}
		rules = append(rules, &RegexRule{regex: regex})
	}

	if config.MinSize > 0 || config.MaxSize > 0 {
		rules = append(rules, &SizeRule{minSize: config.MinSize, maxSize: config.MaxSize})
	}

	if config.JSONField != "" && config.JSONValue != "" {
		jsonValRegex, err := regexp.Compile(config.JSONValue)
		if err != nil {
			return nil, fmt.Errorf("invalid json_value regex for filter stage: %w", err)
		}
		rules = append(rules, &JSONFieldRule{jsonField: config.JSONField, jsonValRegex: jsonValRegex})
	}

	if config.MaxAge > 0 {
		duration := time.Duration(config.MaxAge) * time.Second
		rules = append(rules, &MaxAgeRule{duration: duration})
	}

	s.rules = rules
	return s, nil
}

func (s *FilterStage) Name() string {
	return "filter"
}

// Process applies the filter rules to a log entry.
//
// The logic is as follows:
//  1. Each individual FilterRule's Matches method returns `true` if the log entry
//     satisfies that rule's specific filtering condition (e.g., "is an error", "is too old", "is too large").
//  2. The `s.config.Match` ("any" or "all") determines how the results of individual rules are combined
//     to produce a `finalMatch` boolean.
//     - If `Match == "any"`, `finalMatch` is `true` if *any* rule returns `true`.
//     - If `Match == "all"`, `finalMatch` is `true` only if *all* rules return `true`.
//  3. The `s.config.Action` ("keep" or "drop") then determines the final outcome based on `finalMatch`.
//     - If `Action == "keep"`, the log is kept if `finalMatch` is `true`.
//     - If `Action == "drop"`, the log is kept if `finalMatch` is `false` (i.e., it was *not* matched for dropping).
//
// This design ensures that `finalMatch` directly indicates whether the configured action should be performed.
func (s *FilterStage) Process(entry *models.LogEntry) (bool, error) {
	if len(s.rules) == 0 {
		return true, nil // No rules defined, so we keep the log.
	}

	var finalMatch bool
	if s.config.Match == "any" {
		finalMatch = false
		for _, rule := range s.rules {
			if rule.Matches(entry) {
				finalMatch = true
				break // Short-circuit: if any rule matches, the "any" condition is met.
			}
		}
	} else { // "all"
		finalMatch = true
		for _, rule := range s.rules {
			if !rule.Matches(entry) {
				finalMatch = false
				break // Short-circuit: if any rule does NOT match, the "all" condition is not met.
			}
		}
	}

	// Determine if the log should be kept based on the action and whether a final match occurred.
	// If action is "keep", we keep if a match is found. `shouldKeep = finalMatch`
	// If action is "drop", we keep if NO match is found. `shouldKeep = !finalMatch`
	// This can be expressed as: `(s.config.Action == "keep") == finalMatch`
	shouldKeep := (s.config.Action == "keep") == finalMatch
	return shouldKeep, nil
}
