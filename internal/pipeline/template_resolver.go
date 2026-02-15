package pipeline

import (
	"bytes"
	"fmt"
	"log-enricher/internal/bufferpool"
	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/cespare/xxhash"
	"github.com/mitchellh/mapstructure"
)

// TemplateResolverConfig defines the configuration for the TemplateResolverStage.
type TemplateResolverConfig struct {
	TemplateField string `mapstructure:"template_field"` // The field containing the template string
	ValuesPrefix  string `mapstructure:"values_prefix"`  // Prefix for fields containing values to inject into the template
	OutputField   string `mapstructure:"output_field"`   // The field where the rendered template will be stored
	Placeholder   string `mapstructure:"placeholder"`    // A regex describing the placeholder format (unless it's already in Go template syntax)
}

type fieldCache struct {
	nameInTemplate string
	nameInEntry    []string
}

// TemplateResolverStage is a pipeline stage that resolves Go text templates.
type TemplateResolverStage struct {
	cfg                *TemplateResolverConfig
	templateCache      *cache.PersistedCache[*template.Template]
	templateValuesKeys *cache.PersistedCache[[]fieldCache]
	placeholderRegex   *regexp.Regexp
	mapPool            *bufferpool.ObjectPool[map[string]any]
}

// NewTemplateResolverStage creates a new TemplateResolverStage from the given parameters.
func NewTemplateResolverStage(params map[string]interface{}) (Stage, error) {
	var cfg TemplateResolverConfig
	if err := mapstructure.Decode(params, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode template resolver config: %w", err)
	}

	if cfg.TemplateField == "" {
		return nil, fmt.Errorf("template_field must be specified for template_resolver stage")
	}
	if cfg.ValuesPrefix == "" {
		return nil, fmt.Errorf("values_prefix must be specified for template_resolver stage")
	}
	if cfg.OutputField == "" {
		// Default output field if not specified
		cfg.OutputField = cfg.TemplateField + "_rendered"
	}
	if cfg.Placeholder == "" {
		cfg.Placeholder = `{([a-zA-Z0-9_]+)}`
	}

	// Compile the regex once when the stage is created
	// This regex matches {propertyName} and captures 'propertyName'
	placeholderRegex := regexp.MustCompile(cfg.Placeholder)

	return &TemplateResolverStage{
			cfg:                &cfg,
			templateCache:      cache.NewPersistedCache[*template.Template]("template_resolver_cache", 100, 1000, false),
			templateValuesKeys: cache.NewPersistedCache[[]fieldCache]("template_resolver_values_keys", 100, 1000, false),
			placeholderRegex:   placeholderRegex,
			mapPool: bufferpool.NewObjectPool[map[string]any](
				10,
				func() map[string]any { return make(map[string]any, 5) },
				func(m map[string]any) {
					for k := range m {
						delete(m, k)
					}
				}),
		},
		nil
}

// Name returns the name of the stage.
func (s *TemplateResolverStage) Name() string {
	return "template_resolver"
}

// Process fetches the template, collects values, executes the template, and stores the result.
func (s *TemplateResolverStage) Process(entry *models.LogEntry) (keep bool, err error) {
	// 1. Retrieve the template string
	templateVal, ok := entry.Fields[s.cfg.TemplateField]
	if !ok {
		// If the template field is missing, we can choose to drop the log or just skip this stage.
		// For now, let's skip and keep the log, as it might be optional.
		return true, nil
	}

	templateStr, ok := templateVal.(string)
	if !ok {
		return true, fmt.Errorf("template field '%s' is not a string, got %T", s.cfg.TemplateField, templateVal)
	}

	// 2. Hash the ORIGINAL template string to get a cache key.
	// We use the original string for the cache key because it's the input,
	// and the conversion to Go template syntax is deterministic.
	cacheKey := strconv.FormatUint(xxhash.Sum64String(templateStr), 10)

	// 3a. Try to get the parsed Go template from the cache
	var tmpl *template.Template
	if cachedTemplate, ok := s.templateCache.Get(cacheKey); ok {
		s.templateCache.Hit()
		tmpl = cachedTemplate
	} else {
		s.templateCache.Miss()
		// 3b. Parse the CONVERTED template string if not in cache
		// Convert common template variants (e.g., {name}) to Go template syntax ({{index . "indexed_name"}})
		// This allows the stage to accept templates from other logging frameworks.
		// We use "indexed_" as a prefix to avoid weird Go template handling of numbers as indices
		goTemplateStr := s.placeholderRegex.ReplaceAllString(templateStr, "{{index . \"indexed_$1\"}}")
		tmpl, err = template.New("log_template").Parse(goTemplateStr)
		if err != nil {
			// Include both original and converted string in error for better debugging
			return false, fmt.Errorf("failed to parse template from field '%s' (original: '%s', converted: '%s'): %w", s.cfg.TemplateField, templateStr, goTemplateStr, err)
		}
		s.templateCache.Set(cacheKey, tmpl)
	}

	// 4. Collect data for the template
	templateData := s.mapPool.Acquire()
	defer s.mapPool.Release(templateData)

	if cachedFields, ok := s.templateValuesKeys.Get(cacheKey); ok {
		s.templateValuesKeys.Hit()
		for _, cachedField := range cachedFields {
			templateData[cachedField.nameInTemplate], _ = s.walkFields(entry.Fields, cachedField.nameInEntry, true)
		}

		slog.Debug("TemplateResolverStage: Reusing cached values", "values", templateData, "keys", cachedFields, "prefix", s.cfg.ValuesPrefix)
	} else {
		s.templateValuesKeys.Miss()
		// First try to treat the ValuesPrefix as a map, then as a literal prefix
		keysAtPrefix, prefixPath := s.walkFields(entry.Fields, []string{s.cfg.ValuesPrefix}, false)
		fieldCaches := []fieldCache{}

		if keysMap, ok := keysAtPrefix.(map[string]any); ok {
			for key, val := range keysMap {
				nameInTemplate := "indexed_" + key
				nameInEntry := append(prefixPath, key)
				templateData[nameInTemplate] = val
				fieldCaches = append(fieldCaches, fieldCache{nameInTemplate: nameInTemplate, nameInEntry: nameInEntry})
			}
		} else {
			// If ValuesPrefix is not a map, we have to iterate through all fields and treat it as a literal prefix
			for key, val := range entry.Fields {
				if len(key) > len(s.cfg.ValuesPrefix) && key[:len(s.cfg.ValuesPrefix)] == s.cfg.ValuesPrefix {
					// Extract the property name after the prefix (e.g., "0" from "Properties.0")
					propName := key[len(s.cfg.ValuesPrefix):]
					nameInTemplate := "indexed_" + propName
					nameInEntry := []string{s.cfg.ValuesPrefix + propName}
					templateData[nameInTemplate] = val
					fieldCaches = append(fieldCaches, fieldCache{nameInTemplate: nameInTemplate, nameInEntry: nameInEntry})
				}
			}
		}

		slog.Debug("TemplateResolverStage: Collected values", "values", templateData, "keys", fieldCaches, "prefix", s.cfg.ValuesPrefix)

		s.templateValuesKeys.Set(cacheKey, fieldCaches)
	}

	// 5. Execute the template with the collected data
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		// Include both original and converted string in error for better debugging
		return false, fmt.Errorf("failed to execute template from field '%s': %w", s.cfg.TemplateField, err)
	}

	// 6. Store the rendered output in the specified field
	entry.Fields[s.cfg.OutputField] = buf.String()

	return true, nil
}

// walkFields recursively walks the fields map to find the value for the specified field.
// If cached is true, it will only walk the fields map once.
// It will return a string array (if it's not cached already) indicating the full path to the value
func (s *TemplateResolverStage) walkFields(fields map[string]any, fieldPath []string, cached bool) (any, []string) {
	var val any
	var ok bool

	// Try to get the value at the specified field path
	for _, field := range fieldPath {
		if len(field) == 0 {
			break
		}
		if val, ok = fields[field]; !ok {
			break
		}
	}

	// If the value is found, return it as well as the path to it if it's not cached
	if ok {
		if cached {
			return val, nil
		} else {
			return val, fieldPath
		}
	}

	if !cached {
		// If it's uncached we can assume that the fieldPath only contains one entry
		if len(fieldPath) > 0 {
			field := fieldPath[0]
			if strings.Contains(field, ".") {
				parts := strings.Split(field, ".")
				val = fields
				keys := []string{}
				var mapped map[string]any

				for _, part := range parts {
					// If the part is empty, that likely means the prefix contained a dot as an ending, so we can safely skip that
					if len(part) == 0 {
						break
					}

					if mapped, ok = val.(map[string]any); ok {
						val = mapped[part]
						keys = append(keys, part)
					} else {
						break
					}
				}

				if ok {
					return val, keys
				}
			}
		}
	}

	return nil, nil
}
