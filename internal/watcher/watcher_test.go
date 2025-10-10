package watcher

import (
	"sync"
	"testing"

	"log-enricher/internal/config"
	"log-enricher/internal/models"
)

// Mock Enricher
type mockEnricher struct{}

func (m *mockEnricher) Enrich(ip string) models.Result {
	if ip == "8.8.8.8" {
		return models.Result{
			Hostname: "dns.google",
			Geo:      &models.GeoInfo{Country: "US"},
			Found:    true,
		}
	}
	return models.Result{}
}
func (m *mockEnricher) PerformEnrichment(ip string) models.Result { return m.Enrich(ip) }

// Mock Backend Manager
type mockBackendManager struct {
	mu            sync.Mutex
	lastBroadcast map[string]interface{}
}

func (m *mockBackendManager) Broadcast(sourcePath string, entry map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastBroadcast = entry
}
func (m *mockBackendManager) Shutdown() {}

func TestProcessLine(t *testing.T) {
	lw := &LogWatcher{
		cfg: &config.Config{
			ClientIPFields: []string{"client_ip", "remote_addr"},
		},
		enricher: &mockEnricher{},
	}

	testCases := []struct {
		name             string
		inputLine        string
		expectedHostname string
		expectedCountry  string
		expectedMessage  string
	}{
		{
			name:             "Plain text with IP",
			inputLine:        "Connection from 8.8.8.8",
			expectedHostname: "dns.google",
			expectedCountry:  "US",
			expectedMessage:  "Connection from 8.8.8.8",
		},
		{
			name:             "JSON with client_ip field",
			inputLine:        `{"client_ip": "8.8.8.8", "user": "test"}`,
			expectedHostname: "dns.google",
			expectedCountry:  "US",
			// No message field is expected
		},
		{
			name:             "JSON with IP in message field",
			inputLine:        `{"message": "user logged in from 8.8.8.8"}`,
			expectedHostname: "dns.google",
			expectedCountry:  "US",
			expectedMessage:  "user logged in from 8.8.8.8", // FIX: The message field should be preserved.
		},
		{
			name:            "Plain text without IP",
			inputLine:       "This is a standard log line.",
			expectedMessage: "This is a standard log line.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			backends := &mockBackendManager{}
			lw.backends = backends
			lw.processLine("test.log", tc.inputLine)

			backends.mu.Lock()
			entry := backends.lastBroadcast
			backends.mu.Unlock()

			if entry == nil {
				t.Fatal("backend did not receive a broadcast")
			}

			if h, ok := entry["client_hostname"]; ok && h != tc.expectedHostname {
				t.Errorf("expected hostname %q, got %q", tc.expectedHostname, h)
			}
			if c, ok := entry["client_country"]; ok && c != tc.expectedCountry {
				t.Errorf("expected country %q, got %q", tc.expectedCountry, c)
			}

			// More robust message assertion
			actualMessage, messageExists := entry["message"]
			if tc.expectedMessage != "" {
				if !messageExists {
					t.Errorf("expected a message field with content %q, but it was missing", tc.expectedMessage)
				} else if actualMessage != tc.expectedMessage {
					t.Errorf("expected message %q, got %q", tc.expectedMessage, actualMessage)
				}
			} else if messageExists {
				t.Errorf("expected no message field, but it existed with content: %q", actualMessage)
			}
		})
	}
}
