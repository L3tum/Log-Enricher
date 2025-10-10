package backends

import (
	"log"
	"strings"

	"log-enricher/internal/config"
)

// Manager is the public interface for a backend manager that can broadcast entries and be shut down.
type Manager interface {
	Broadcast(sourcePath string, entry map[string]interface{})
	Shutdown()
}

// manager is the concrete, unexported implementation of the Manager interface.
type manager struct {
	backends []Backend
}

// NewManager initializes backends based on the application configuration and returns the Manager interface.
func NewManager(cfg *config.Config) (Manager, error) {
	m := &manager{}
	var enabledBackends []string

	backendSet := make(map[string]bool)
	for _, b := range cfg.Backends {
		backendSet[strings.TrimSpace(strings.ToLower(b))] = true
	}

	if backendSet["file"] {
		m.backends = append(m.backends, NewFileBackend(cfg.EnrichedFileSuffix))
		enabledBackends = append(enabledBackends, "file")
	}

	if backendSet["loki"] {
		lokiBackend, err := NewLokiBackend(cfg.LokiURL)
		if err != nil {
			return nil, err // Propagate critical init errors
		}
		if lokiBackend != nil {
			m.backends = append(m.backends, lokiBackend)
			enabledBackends = append(enabledBackends, "loki")
		}
	}

	if len(enabledBackends) == 0 {
		log.Println("Warning: No backends enabled. Enriched logs will be discarded.")
	} else {
		log.Printf("Enabled backends: %s", strings.Join(enabledBackends, ", "))
	}
	return m, nil
}

// Broadcast sends the log entry to all enabled backends.
func (m *manager) Broadcast(sourcePath string, entry map[string]interface{}) {
	for _, b := range m.backends {
		if err := b.Send(sourcePath, entry); err != nil {
			log.Printf("Error sending to backend '%s': %v", b.Name(), err)
		}
	}
}

// Shutdown gracefully stops all managed backends.
func (m *manager) Shutdown() {
	log.Println("Shutting down backends...")
	for _, b := range m.backends {
		b.Shutdown()
	}
}
