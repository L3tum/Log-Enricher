package backends

import (
	"fmt"
	"log-enricher/internal/models"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/grafana/dskit/flagext"
	"github.com/grafana/loki-client-go/loki"
	"github.com/grafana/loki-client-go/pkg/urlutil"
	"github.com/prometheus/common/model"
)

const defaultLokiPushPath = "/loki/api/v1/push"

// LokiBackend sends enriched logs to a Grafana Loki instance.
type LokiBackend struct {
	client *loki.Client
}

// NewLokiBackend creates a new Loki backend.
func NewLokiBackend(lokiURL string) (*LokiBackend, error) {
	u, err := normalizeLokiPushURL(lokiURL)
	if err != nil {
		return nil, err
	}

	// Wait for Loki to become ready before creating a client.
	// This prevents a race condition on startup where this service starts faster than Loki.
	readyURL := *u
	readyURL.Path = "/ready"
	slog.Debug("Waiting for Loki to be ready", "url", readyURL.String())
	isConnected := false

	httpClient := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 30; i++ { // Retry for ~60 seconds
		resp, err := httpClient.Get(readyURL.String())
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			slog.Debug("Loki is ready.")
			isConnected = true
			break
		}

		if err != nil {
			slog.Warn("Loki readiness check failed", "error", err, "retrying", true)
		} else {
			slog.Warn("Loki readiness check failed", "status", resp.Status, "retrying", true)
			resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}

	if !isConnected {
		return nil, fmt.Errorf("loki did not become ready in time")
	}

	cfg := loki.Config{
		URL:       urlutil.URLValue(flagext.URLValue{URL: u}),
		Timeout:   5 * time.Second,
		BatchSize: 100,
	}

	client, err := loki.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Loki client: %w", err)
	}

	slog.Info("Loki backend enabled, sending logs to", "url", lokiURL)

	b := &LokiBackend{
		client: client,
	}
	return b, nil
}

func normalizeLokiPushURL(lokiURL string) (*url.URL, error) {
	if lokiURL == "" {
		return nil, fmt.Errorf("loki URL is empty")
	}

	u, err := url.Parse(lokiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Loki URL: %w", err)
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = defaultLokiPushPath
	}

	return u, nil
}

func (b *LokiBackend) Name() string {
	return "loki"
}

// Send now accepts a LogEntry struct directly.
func (b *LokiBackend) Send(entry *models.LogEntry) error {
	// Use the App field from the LogEntry for the 'app' label.
	// Fallback to "log-enricher" if App is empty.
	appName := strings.Clone(entry.App)
	if appName == "" {
		appName = "log-enricher"
	}

	labels := model.LabelSet{
		"job":         "log-enricher",
		"source_file": model.LabelValue(strings.Clone(filepath.Base(entry.SourcePath))),
		"app":         model.LabelValue(appName),
	}

	// Manually copy the timestamp struct.
	// This creates an independent copy of the time.Time value.
	timestampCopy := entry.Timestamp

	// If there are no fields (no JSON) send the log line as the log message
	if len(entry.Fields) == 0 {
		return b.client.Handle(labels, timestampCopy, string(entry.LogLine))
	}

	// Marshal the Fields map to JSON for the log line content.
	// This ensures all processed fields, including any added by pipeline stages, are sent.
	entryAsBytes, err := json.MarshalWithOption(entry.Fields, json.UnorderedMap())
	if err != nil {
		slog.Error("Failed to marshal log entry fields to JSON", "error", err)
		return fmt.Errorf("failed to marshal log entry fields to JSON: %w", err)
	}

	return b.client.Handle(labels, timestampCopy, string(entryAsBytes))
}

// CloseWriter is a no-op for LokiBackend as it doesn't manage per-file resources.
func (b *LokiBackend) CloseWriter(sourcePath string) {
	// No-op for LokiBackend
	slog.Debug("CloseWriter called on LokiBackend (no-op)", "source_path", sourcePath)
}

// Shutdown stops the Loki client, which flushes any buffered entries.
func (b *LokiBackend) Shutdown() {
	if b.client != nil {
		b.client.Stop()
		slog.Info("Loki backend shut down.")
	}
}
