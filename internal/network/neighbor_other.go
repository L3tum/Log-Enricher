//go:build !linux

package network

import (
	"context"
	"log/slog"
)

// WatchNeighbors is a no-op stub for non-Linux systems.
func WatchNeighbors(ctx context.Context) {
	slog.Debug("Neighbor watcher is not supported on this OS, skipping.")
}

// TriggerNeighborDiscovery is a no-op stub for non-Linux systems.
func TriggerNeighborDiscovery(ipStr string) {
	slog.Debug("Neighbor discovery is not supported on this OS, skipping.")
}
