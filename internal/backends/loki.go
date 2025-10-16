package backends

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/grafana/dskit/flagext"
	"github.com/grafana/loki-client-go/loki"
	"github.com/grafana/loki-client-go/pkg/urlutil"
	"github.com/prometheus/common/model"
)

// LokiBackend sends enriched logs to a Grafana Loki instance.
type LokiBackend struct {
	client *loki.Client
}

// NewLokiBackend creates a new Loki backend. Returns nil if the URL is not provided.
func NewLokiBackend(lokiURL string) (*LokiBackend, error) {
	if lokiURL == "" {
		return nil, nil // Backend is disabled if no URL is set.
	}

	var u *url.URL
	var err error

	u, err = url.Parse(lokiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Loki URL: %w", err)
	}

	// Wait for Loki to become ready before creating a client.
	// This prevents a race condition on startup where this service starts faster than Loki.
	readyURL := *u
	readyURL.Path = "/ready"
	log.Printf("Waiting for Loki to be ready at %s...", readyURL.String())
	isConnected := false

	httpClient := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 15; i++ { // Retry for ~30 seconds
		resp, err := httpClient.Get(readyURL.String())
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			log.Println("Loki is ready.")
			isConnected = true
			break
		}

		if err != nil {
			log.Printf("Loki readiness check failed: %v. Retrying...", err)
		} else {
			log.Printf("Loki readiness check failed with status: %s. Retrying...", resp.Status)
			resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}

	if !isConnected {
		return nil, fmt.Errorf("loki did not become ready in time")
	}

	cfg := loki.Config{
		URL:     urlutil.URLValue(flagext.URLValue{URL: u}),
		Timeout: 5 * time.Second,
	}

	client, err := loki.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Loki client: %w", err)
	}

	log.Println("Loki backend enabled, sending logs to", lokiURL)
	return &LokiBackend{client: client}, nil
}

func (b *LokiBackend) Name() string {
	return "loki"
}

// Send uses the pre-marshaled entry and sends it to Loki.
func (b *LokiBackend) Send(sourcePath string, entryAsBytes []byte) error {
	// Use the sourcePath's filename as a label for better context in Loki.
	appName := filepath.Base(filepath.Dir(sourcePath))
	if appName == "." || appName == "/" || appName == "" {
		appName = "log-enricher"
	}
	labels := model.LabelSet{
		"job":         "log-enricher",
		"source_file": model.LabelValue(filepath.Base(sourcePath)),
		"app":         model.LabelValue(appName),
	}

	// The JSON encoder adds a newline, which we must trim before sending to Loki.
	line := bytes.TrimSuffix(entryAsBytes, []byte("\n"))

	// The Loki client handles batching and sending internally.
	err := b.client.Handle(labels, time.Now(), string(line))
	if err != nil {
		return err
	}
	return nil
}

// Shutdown stops the Loki client, which flushes any buffered entries.
func (b *LokiBackend) Shutdown() {
	if b.client != nil {
		b.client.Stop()
	}
}
