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
