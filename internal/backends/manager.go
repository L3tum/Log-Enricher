package backends

import (
	"log"
	"log-enricher/internal/bufferpool"
	"strings"

	"log-enricher/internal/config"

	"github.com/goccy/go-json"
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

// Broadcast marshals the entry to JSON once and sends the resulting bytes to all enabled backends.
func (m *manager) Broadcast(sourcePath string, entry map[string]interface{}) {
	if len(m.backends) == 0 {
		return
	}

	// GetByteBuffer a buffer from the pool.
	buf := bufferpool.GetByteBuffer()
	// Ensure the buffer is returned to the pool when we're done.
	defer bufferpool.PutByteBuffer(buf)

	// Use a JSON encoder to write directly into the buffer, which is more efficient.
	// Note: The encoder automatically adds a newline character.
	if err := json.NewEncoder(buf).Encode(entry); err != nil {
		log.Printf("Error marshaling log entry for backends: %v", err)
		return
	}

	lineBytes := buf.Bytes()

	for _, b := range m.backends {
		if err := b.Send(sourcePath, lineBytes); err != nil {
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
