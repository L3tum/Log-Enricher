//go:build !linux

package network

import "log"

// WatchNeighbors is a no-op stub for non-Linux systems.
func WatchNeighbors() {
	log.Println("Neighbor watcher is not supported on this OS, skipping.")
}
