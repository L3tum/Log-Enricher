package network

import (
	"log"
	"net"
	"sync"
	"time"

	"log-enricher/internal/models"
)

var (
	ipToMacMu sync.RWMutex
	ipToMac   = make(map[string]string)

	macToDevMu sync.RWMutex
	macToDev   = make(map[string]*models.DeviceInfo)
)

// GetMACForIP safely retrieves a MAC address for a given IP from the cache.
func GetMACForIP(ip string) string {
	ipToMacMu.RLock()
	defer ipToMacMu.RUnlock()
	return ipToMac[ip]
}

// BorrowHostnameViaMAC safely retrieves a hostname using a shared MAC address.
func BorrowHostnameViaMAC(mac string) string {
	macToDevMu.RLock()
	defer macToDevMu.RUnlock()
	if dev := macToDev[mac]; dev != nil {
		return dev.Hostname
	}
	return ""
}

// StoreHostnameForMAC safely stores a hostname for a given MAC address.
func StoreHostnameForMAC(mac, hostname string) {
	if mac == "" || hostname == "" {
		return
	}
	macToDevMu.Lock()
	defer macToDevMu.Unlock()
	dev := macToDev[mac]
	if dev == nil {
		dev = models.NewDeviceInfo()
	}
	dev.Hostname = hostname
	macToDev[mac] = dev
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
		log.Printf("Error in triggerNeighborDiscovery for %s: %v", ipStr, err)
		return
	}
	_ = conn.Close()
	// Give the kernel a moment to populate the neighbor table.
	time.Sleep(200 * time.Millisecond)
}
