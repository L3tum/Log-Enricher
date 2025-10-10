//go:build linux

package network

import (
	"log"
	"strings"

	"log-enricher/internal/models"

	"github.com/vishvananda/netlink"
)

// WatchNeighbors listens for ARP and NDP updates from the kernel's netlink interface.
// This function will only be compiled on Linux.
func WatchNeighbors() {
	ch := make(chan netlink.NeighUpdate)
	done := make(chan struct{})
	if err := netlink.NeighSubscribe(ch, done); err != nil {
		log.Printf("failed to subscribe to neighbor updates: %v", err)
		return
	}
	log.Println("Starting neighbor watcher (Linux-only feature)")

	for update := range ch {
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

		log.Printf("Neighbor update: %s -> %s", ip, mac)
	}
}
