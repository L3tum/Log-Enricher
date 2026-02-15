package pipeline

import (
	"bytes"
	"fmt"
	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log/slog"
	"regexp"
	"strconv"

	"github.com/cespare/xxhash"
	"github.com/mitchellh/mapstructure"
)

type StructuredParserConfig struct {
	Pattern string `mapstructure:"pattern"`
}

// StructuredParser parses log lines using a regular expression to extract key-value pairs.
type StructuredParser struct {
	regex                *regexp.Regexp
	successfulParseCache *cache.PersistedCache[bool]
}

func (p *StructuredParser) Name() string {
	return "structured_parser"
}

// NewStructuredParser creates a new StructuredParser with the given regex pattern.
// It returns an error if the regex pattern is invalid.
func NewStructuredParser(params map[string]any) (Stage, error) {
	stageConfig := StructuredParserConfig{}
	if err := mapstructure.Decode(params, &stageConfig); err != nil {
		return nil, fmt.Errorf("failed to decode structured log parser config: %w", err)
	}

	if len(stageConfig.Pattern) == 0 {
		stageConfig.Pattern = `(\w+)=(".*?"|\S+)` // Default regex for key="value with spaces" or key=valueWithoutSpaces
		slog.Info("No structured log regex pattern configured, using default", "pattern", stageConfig.Pattern)
	}

	regex, err := regexp.Compile(stageConfig.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid structured log regex pattern: %w", err)
	}

	// If there are no named capture groups, we expect it to be a key-value pair pattern with 2 groups.
	// SubexpNames() includes the full match as the first element.
	if len(regex.SubexpNames()) <= 1 && regex.NumSubexp() != 2 {
		return nil, fmt.Errorf("regex must have named capture groups, or exactly 2 unnamed capture groups for key-value pairs")
	}

	hash := strconv.FormatUint(xxhash.Sum64String(stageConfig.Pattern), 10)

	return &StructuredParser{
		regex:                regex,
		successfulParseCache: cache.NewPersistedCache[bool]("structured_parser_"+hash, 10, 100, true),
	}, nil
}

// Process extracts structured data from a log line based on the configured regex.
func (p *StructuredParser) Process(logEntry *models.LogEntry) (bool, error) {
	// Skip any log lines that already have fields (another parser was successful)
	if len(logEntry.Fields) > 0 {
		return true, nil
	}

	cacheKey := logEntry.SourcePath + ":empty"
	if len(logEntry.LogLine) > 0 {
		cacheKey = logEntry.SourcePath + string(logEntry.LogLine[0])
	}

	var ok bool
	var success bool
	if success, ok = p.successfulParseCache.Get(cacheKey); ok && !success {
		p.successfulParseCache.Hit()
		return true, nil
	}

	// If the regex has named capture groups, use the named group parsing strategy.
	if len(p.regex.SubexpNames()) > 1 {
		if !p.parseWithNamedGroups(logEntry) {
			// If the regex didn't match, clear any fields that were partially parsed
			for k := range logEntry.Fields {
				delete(logEntry.Fields, k)
			}

			// Switch cache entry to false and register a miss if it was previously true
			p.successfulParseCache.Set(cacheKey, false)
			if ok {
				p.successfulParseCache.Miss()
			}
		} else if !ok {
			// Set the cache entry to true if it wasn't previously set
			p.successfulParseCache.Set(cacheKey, true)
		} else {
			// Register a hit if the cache was set and predicted a match
			p.successfulParseCache.Hit()
		}

		// Always keep the log line
		return true, nil
	}

	// Otherwise, use the iterative key-value pair parsing strategy.
	if !p.parseKeyValuePairs(logEntry) {
		// If the regex didn't match, clear any fields that were partially parsed
		for k := range logEntry.Fields {
			delete(logEntry.Fields, k)
		}

		// Switch log entry to false and register a miss if it was previously true
		p.successfulParseCache.Set(cacheKey, false)
		if ok {
			p.successfulParseCache.Miss()
		}
	} else if !ok {
		// Set the cache entry to true if it wasn't previously set
		p.successfulParseCache.Set(cacheKey, true)
	} else {
		// Register a hit if the cache was set and predicted a match
		p.successfulParseCache.Hit()
	}

	// Always keep the log line
	return true, nil
}

// parseWithNamedGroups matches the log line against a regex with named capture groups.
func (p *StructuredParser) parseWithNamedGroups(logEntry *models.LogEntry) bool {
	match := p.regex.FindSubmatch(logEntry.LogLine)
	if match == nil {
		return false
	}

	for i, name := range p.regex.SubexpNames() {
		// Index 0 is the full match, and we only care about named groups.
		if i != 0 && name != "" {
			logEntry.Fields[name] = string(match[i])
		}
	}

	return true
}

// parseKeyValuePairs extracts key-value pairs from a log line based on the configured regex.
func (p *StructuredParser) parseKeyValuePairs(logEntry *models.LogEntry) bool {
	unparsedParts := false
	lastIndex := 0
	lineBytes := logEntry.LogLine

	matches := p.regex.FindAllSubmatchIndex(lineBytes, -1)

	if len(matches) == 0 {
		return false
	}

	for _, match := range matches {
		// Break early on unmatched text
		if match[0] > lastIndex {
			// But only if it's not spaces
			if len(bytes.TrimSpace(lineBytes[lastIndex:match[0]])) > 0 {
				slog.Debug("Unmatched text found at start in log line", "unmatchedText", string(lineBytes[lastIndex:match[0]]))
				unparsedParts = true
				break
			}
		}

		// Extract key and value
		key := lineBytes[match[2]:match[3]]   // The first capture group (key)
		value := lineBytes[match[4]:match[5]] // The second capture group (value)

		// Remove quotes from value if present
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}

		logEntry.Fields[string(key)] = string(value)
		lastIndex = match[1] // Update lastIndex to the end of the current match
	}

	// Unsuccessful if there's anything remaining
	if lastIndex < len(lineBytes) {
		// But only if it's not spaces
		if len(bytes.TrimSpace(lineBytes[lastIndex:])) > 0 {
			slog.Debug("Unmatched text found at end in log line", "unmatchedText", string(lineBytes[lastIndex:]))
			unparsedParts = true
		}
	}

	if unparsedParts {
		return false
	}

	return true
}
