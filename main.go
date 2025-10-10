package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"log-enricher/internal/backends"
	"log-enricher/internal/cache"
	"log-enricher/internal/config"
	"log-enricher/internal/enrichment"
	"log-enricher/internal/logging"
	"log-enricher/internal/network"
	"log-enricher/internal/requery"
	"log-enricher/internal/state"
	"log-enricher/internal/watcher"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Initialize unified state
	if err := state.Initialize(cfg.StateFilePath); err != nil {
		log.Fatalf("Failed to initialize state: %v", err)
	}

	// Initialize Backends
	backendManager, err := backends.NewManager(cfg)
	if err != nil {
		// Logging before this point will go to the default stdout.
		log.Fatalf("Failed to initialize backends: %v", err)
	}

	// Hijack the standard logger to send all logs to backends and stdout.
	logging.New(backendManager)

	// Initialize cache
	if err := cache.Initialize(cfg.CacheSize); err != nil {
		log.Fatalf("Failed to initialize cache: %v", err)
	}

	// Load cache from state
	if err := cache.LoadFromState(); err != nil {
		log.Printf("Warning: Failed to load cache from state: %v", err)
	}

	// Dynamically create the enrichment pipeline based on config.
	var stages []enrichment.Stage
	if cfg.EnableHostnameStage {
		stages = append(stages, enrichment.NewHostnameStage(cfg))
	}
	if cfg.EnableGeoIpStage {
		// NewGeoIPStage gracefully returns nil if the path is invalid.
		// We must check for nil before appending to the interface slice to avoid a panic.
		if geoIPStage, err := enrichment.NewGeoIPStage(cfg.GeoIPDatabasePath); err == nil {
			stages = append(stages, geoIPStage)
		} else {
			log.Printf("Warning: Failed to initialize GeoIP stage: %v", err)
		}
	}
	if cfg.EnableCrowdsecStage {
		if crowdsecStage, err := enrichment.NewCrowdsecStage(cfg); err == nil {
			stages = append(stages, crowdsecStage)
		} else {
			log.Printf("Warning: Failed to initialize Crowdsec stage: %v", err)
		}
	}
	enrichmentPipeline := enrichment.NewPipeline(stages...)

	// Start neighbor watcher
	go network.WatchNeighbors()

	// Start log file watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := watcher.StartLogWatcher(ctx, cfg, enrichmentPipeline, backendManager); err != nil {
		log.Fatalf("Failed to start log watcher: %v", err)
	}

	// Start cache requery goroutine
	go requery.StartRequeryLoop(cfg.RequeryInterval, enrichmentPipeline)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutting down gracefully...")

	// 1. Signal all background goroutines to stop.
	cancel()

	// 2. Persist all in-memory state to disk first.
	// Sync cache to state before saving.
	if err := cache.SaveToState(); err != nil {
		log.Printf("Error syncing cache to state: %v", err)
	}

	// Save unified state (includes file metadata, positions, and cache).
	if err := state.Save(cfg.StateFilePath); err != nil {
		log.Printf("Error saving state: %v", err)
	}

	// 3. Shutdown backends (like logging) only after all other work is done.
	backendManager.Shutdown()

	log.Println("Shutdown complete")
}
