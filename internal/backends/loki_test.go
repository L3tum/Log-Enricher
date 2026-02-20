package backends

import (
	"strings"
	"testing"
)

func TestNewLokiBackend_EmptyURLReturnsError(t *testing.T) {
	backend, err := NewLokiBackend("")
	if err == nil {
		t.Fatal("expected error for empty Loki URL")
	}
	if backend != nil {
		t.Fatal("expected nil backend for empty Loki URL")
	}
	if !strings.Contains(err.Error(), "loki URL is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeLokiPushURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantURL string
	}{
		{
			name:    "base url gets default push path",
			input:   "http://loki:3100",
			wantURL: "http://loki:3100/loki/api/v1/push",
		},
		{
			name:    "root path gets default push path",
			input:   "http://loki:3100/",
			wantURL: "http://loki:3100/loki/api/v1/push",
		},
		{
			name:    "explicit push path preserved",
			input:   "http://loki:3100/loki/api/v1/push",
			wantURL: "http://loki:3100/loki/api/v1/push",
		},
		{
			name:    "custom path preserved",
			input:   "http://loki:3100/custom/push",
			wantURL: "http://loki:3100/custom/push",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeLokiPushURL(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.String() != tc.wantURL {
				t.Fatalf("expected %s, got %s", tc.wantURL, got.String())
			}
		})
	}
}
