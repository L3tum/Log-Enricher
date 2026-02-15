//go:build linux

package network

import (
	"context" // Import context package
	"log/slog"
	"net"
	"strings"
	"time"

	"log-enricher/internal/models"

	"github.com/vishvananda/netlink"
)

// WatchNeighbors listens for ARP and NDP updates from the kernel's netlink interface.
// This function will only be compiled on Linux. It accepts a context.Context
// to allow for graceful shutdown of the watcher.
func WatchNeighbors(ctx context.Context) {
	ch := make(chan netlink.NeighUpdate)
	done := make(chan struct{})
	if err := netlink.NeighSubscribe(ch, done); err != nil {
		slog.Error("failed to subscribe to neighbor updates", "error", err)
		return
	}
	slog.Info("Starting neighbor watcher (Linux-only feature)")

	// Step 2: Integrate Context for Cancellation
	for {
		select {
		case update := <-ch:
			ip := update.IP.String()
			if ip == "" || update.HardwareAddr == nil {
				continue
			}
			mac := update.HardwareAddr.String()

			ipToMacMu.Lock()
			ipToMac[ip] = mac
			ipToMacMu.Unlock()

			macToDevMu.Lock()
			dev := macToDev[mac]
			if dev == nil {
				dev = models.NewDeviceInfo()
				macToDev[mac] = dev
			}
			if strings.Contains(ip, ":") {
				dev.IPv6s[ip] = struct{}{}
			} else {
				dev.IPv4s[ip] = struct{}{}
			}
			macToDevMu.Unlock()

			slog.Debug("Neighbor update", "ip", ip, "mac", mac)
		case <-ctx.Done(): // Listen for context cancellation
			slog.Info("Stopping neighbor watcher due to context cancellation.")
			close(done) // Signal netlink.NeighSubscribe to stop
			return
		}
	}
}

// TriggerNeighborDiscovery sends a UDP packet to an IPv6 address to stimulate a Neighbor Advertisement.
func TriggerNeighborDiscovery(ipStr string) {
	dst := net.ParseIP(ipStr)
	if dst == nil || dst.To4() != nil {
		return
	}
	// Try to open a UDP connection to a high port; kernel will send NS.
	addr := &net.UDPAddr{IP: dst, Port: 65535}
	conn, err := net.DialUDP("udp6", nil, addr)
	if err != nil {
		slog.Error("Error in triggerNeighborDiscovery", "ip", ipStr, "error", err)
		return
	}
	_ = conn.Close()
	// Give the kernel a moment to populate the neighbor table.
	time.Sleep(200 * time.Millisecond)
}
