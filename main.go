package main

import (
	"context"
	"fmt"
	"log-enricher/internal/pipeline"
	"log-enricher/internal/tailer"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"log-enricher/internal/backends"
	"log-enricher/internal/config"
	"log-enricher/internal/logging"
	"log-enricher/internal/state"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Create a context for the application's lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("Shutting down gracefully...")
		cancel() // Signal runApplication to stop
	}()

	if err := runApplication(ctx, cfg); err != nil {
		slog.Error("Application failed", "error", err)
		os.Exit(1)
	}

	slog.Info("Shutdown complete")
}

// runApplication contains the core logic of the log-enricher application.
// It takes a context for graceful shutdown and the application configuration.
func runApplication(ctx context.Context, cfg *config.Config) error {
	// Initialize unified state
	if err := state.Initialize(cfg.StateFilePath); err != nil {
		return fmt.Errorf("failed to initialize state: %w", err)
	}

	// Initialize Backend
	var backend backends.Backend
	if cfg.Backend == "file" {
		backend = backends.NewFileBackend(cfg.EnrichedFileSuffix)
	} else if cfg.Backend == "loki" {
		if cfg.LokiURL == "" {
			return fmt.Errorf("LOKI_URL must be configured when BACKEND=loki")
		}

		var err error
		backend, err = backends.NewLokiBackend(cfg.LokiURL)

		if err != nil {
			return fmt.Errorf("failed to initialize Loki backend: %w", err)
		}
	} else {
		return fmt.Errorf("backend %s not supported", cfg.Backend)
	}

	// Hijack the standard logger to send all logs to backends and stdout.
	logging.New(cfg.LogLevel, filepath.Clean(cfg.LogBasePath+"/log-enricher/process.log"), backend)

	// Create pipeline stages
	pipelineManager, err := pipeline.NewManager(cfg, ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize pipeline: %w", err)
	}

	// Create the log manager
	manager, err := tailer.NewManagerImpl(cfg, pipelineManager, backend)

	if err != nil {
		return fmt.Errorf("failed to initialize log manager: %w", err)
	}

	if err := manager.StartWatching(ctx); err != nil {
		return fmt.Errorf("failed to start log watcher: %w", err)
	}

	// Wait for the context to be cancelled (e.g., by signal handler or test)
	<-ctx.Done()
	slog.Info("Context cancelled, initiating shutdown sequence...")

	// Save unified state (includes file metadata, positions, and cache).
	if err := state.Save(cfg.StateFilePath); err != nil {
		slog.Error("Error saving state", "error", err)
	}

	// 2. Shutdown backends (like logging) only after all other work is done.
	backend.Shutdown()

	return nil
}
