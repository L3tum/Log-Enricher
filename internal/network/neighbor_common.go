package network

import (
	"log-enricher/internal/models"
	"sync"
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
