package logging

import (
	"context"
	"fmt"
	"log-enricher/internal/backends"
	"log-enricher/internal/bufferpool"
	"log/slog"
	"os"
	"strings"
)

// BackendHandler is a custom slog.Handler that writes to os.Stdout and sends to a backend.
type BackendHandler struct {
	slog.Handler // Embed a default handler for console output
	backend      backends.Backend
	minLevel     slog.Level
	sourcePath   string
}

// NewBackendHandler creates a new BackendHandler.
func NewBackendHandler(sourcePath string, backend backends.Backend, minLevel slog.Level) *BackendHandler {
	// Create a default text handler for console output.
	// We'll use this embedded handler to format and write to os.Stdout.
	opts := &slog.HandlerOptions{
		AddSource: false, // Typically not needed for console output in this setup
		Level:     minLevel,
	}
	textHandler := slog.NewTextHandler(os.Stdout, opts)

	return &BackendHandler{
		sourcePath: sourcePath,
		Handler:    textHandler,
		backend:    backend,
		minLevel:   minLevel,
	}
}

// Enabled reports whether the handler handles records at the given level.
// This is crucial for filtering messages before they are processed by Handle.
func (h *BackendHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// The embedded handler decides if it's enabled for console output.
	// Our custom logic decides if it's enabled for backend sending.
	return h.Handler.Enabled(ctx, level) || level >= h.minLevel
}

// Handle processes the slog.Record.
func (h *BackendHandler) Handle(ctx context.Context, r slog.Record) error {
	// 1. First, let the embedded handler write to os.Stdout.
	// This ensures console output is always present regardless of backend sending.
	if err := h.Handler.Handle(ctx, r); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to write log to stdout: %v\n", err)
	}

	// 2. Conditionally send to the backend based on minLevel.
	if r.Level >= h.minLevel {
		logEntry := bufferpool.LogEntryPool.Acquire()
		defer bufferpool.LogEntryPool.Release(logEntry)
		logEntry.Fields["level"] = r.Level.String()
		logEntry.Fields["message"] = r.Message
		logEntry.Fields["source"] = "internal" // Indicates this is an internal application log
		logEntry.Timestamp = r.Time
		logEntry.SourcePath = h.sourcePath
		logEntry.App = "log-enricher" // Default app name for internal logs

		// Add all attributes from the slog.Record to the LogEntry.Fields map.
		r.Attrs(func(attr slog.Attr) bool {
			logEntry.Fields[attr.Key] = attr.Value.Any()
			return true
		})

		err := h.backend.Send(logEntry)
		if err != nil {
			return err
		}
	}

	return nil
}

// WithAttrs returns a new BackendHandler whose attributes consist of
// the receiver's attributes followed by the argument's.
func (h *BackendHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &BackendHandler{
		Handler:  h.Handler.WithAttrs(attrs),
		backend:  h.backend,
		minLevel: h.minLevel,
	}
}

// WithGroup returns a new BackendHandler with the given group appended to the receiver's existing groups.
func (h *BackendHandler) WithGroup(name string) slog.Handler {
	return &BackendHandler{
		Handler:  h.Handler.WithGroup(name),
		backend:  h.backend,
		minLevel: h.minLevel,
	}
}

// New creates and configures the global slog logger.
func New(envLogLevel string, sourcePath string, backend backends.Backend) {
	var level slog.Level
	switch strings.ToUpper(envLogLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo // Default to INFO
	}

	// Create our custom handler
	backendHandler := NewBackendHandler(sourcePath, backend, level)

	// Set the global default logger to use our custom handler
	slog.SetDefault(slog.New(backendHandler))
}
