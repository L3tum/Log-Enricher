package pipeline

import (
	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log/slog"

	"github.com/goccy/go-json"
)

type JSONParser struct {
	successfulParseCache *cache.PersistedCache[bool]
}

func (p *JSONParser) Name() string {
	return "json_parser"
}

func NewJSONParser() Stage {
	return &JSONParser{
		successfulParseCache: cache.NewPersistedCache[bool]("json_parser", 10, 100, true),
	}
}

func (p *JSONParser) Process(logEntry *models.LogEntry) (bool, error) {
	// Skip any log lines that already have fields (another parser was successful)
	if len(logEntry.Fields) > 0 {
		return true, nil
	}

	cacheKey := logEntry.SourcePath + string(logEntry.LogLine[0])

	var success bool
	var ok bool
	if success, ok = p.successfulParseCache.Get(cacheKey); ok && !success {
		p.successfulParseCache.Hit()
		return true, nil
	}

	if len(logEntry.LogLine) > 0 && logEntry.LogLine[0] == byte('{') {
		// Attempt JSON unmarshaling
		slog.Debug("Line appears to be JSON", "path", logEntry.SourcePath)
		err := json.Unmarshal(logEntry.LogLine, &(logEntry.Fields))

		// On error Unmarshal sets the pointer to nil, which is very helpful :)
		// IMPORTANT: We can just overwrite the fields here since it's unlikely anything else set any fields before a parser ran
		if err != nil {
			logEntry.Fields = make(map[string]interface{})

			p.successfulParseCache.Set(cacheKey, false)
			if ok {
				p.successfulParseCache.Miss()
			}
		} else if ok {
			p.successfulParseCache.Hit()
		} else {
			p.successfulParseCache.Set(cacheKey, true)
		}
	} else {
		p.successfulParseCache.Set(cacheKey, false)
		if ok {
			p.successfulParseCache.Miss()
		}
	}

	// Always return true to indicate to keep the log line
	return true, nil
}
