package enrichment

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"log-enricher/internal/models"
	"log-enricher/internal/network"
)

// HostnameConfig holds the configuration for the hostname enrichment stage.
type HostnameConfig struct {
	DNSServer     string `mapstructure:"dns_server"`
	EnableRDNS    bool   `mapstructure:"enable_rdns"`
	EnableMDNS    bool   `mapstructure:"enable_mdns"`
	EnableLLMNR   bool   `mapstructure:"enable_llmnr"`
	EnableNetBIOS bool   `mapstructure:"enable_netbios"`
}

// HostnameStage performs various network lookups in parallel to find a hostname.
type HostnameStage struct {
	cfg      *HostnameConfig
	resolver *net.Resolver
}

// NewHostnameStage creates a new, parallel hostname enrichment stage.
func NewHostnameStage(config *HostnameConfig) *HostnameStage {
	return &HostnameStage{
		cfg:      config,
		resolver: network.CustomResolver(config.DNSServer),
	}
}

func (s *HostnameStage) Name() string {
	return "hostname"
}

// Run executes all configured hostname lookups concurrently and returns the first result.
func (s *HostnameStage) Run(ip string, result *models.Result) (updated bool) {
	isIPv6 := strings.Contains(ip, ":")
	initialHostname := result.Hostname
	initialMAC := result.MAC

	// Pre-fill MAC if we already know it.
	if mac := network.GetMACForIP(ip); mac != "" {
		result.MAC = mac
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // Overall timeout
	defer cancel()

	var wg sync.WaitGroup
	var once sync.Once
	foundHost := make(chan string, 1)

	// --- Define Lookup Functions ---
	runLookup := func(lookupFunc func() string) {
		defer wg.Done()
		if host := lookupFunc(); host != "" {
			// Use sync.Once to ensure we only send the first result.
			once.Do(func() {
				foundHost <- host
			})
		}
	}

	// --- Launch Lookups in Parallel ---
	if s.cfg.EnableRDNS {
		wg.Add(1)
		go runLookup(func() string { return network.ResolveRDNS(ip, s.resolver) })
	}
	if s.cfg.EnableMDNS {
		wg.Add(1)
		go runLookup(func() string { return network.QueryMDNS(ip) })
	}
	if !isIPv6 && s.cfg.EnableNetBIOS {
		wg.Add(1)
		go runLookup(func() string { return network.QueryNetBIOSName(ip) })
	}
	if s.cfg.EnableLLMNR {
		wg.Add(1)
		go runLookup(func() string { return network.QueryLLMNR(ip) })
	}

	// Wait for a result or timeout
	select {
	case host := <-foundHost:
		result.Hostname = host
	case <-ctx.Done():
		// All lookups timed out or failed.
	}

	// Close the channel and wait for all goroutines to finish to prevent leaks.
	close(foundHost)
	go func() {
		wg.Wait()
	}()

	// --- Post-lookup MAC-based logic (if no hostname was found) ---
	if result.Hostname == "" {
		if isIPv6 && result.MAC == "" {
			if mac, ok := network.MacFromEUI64(net.ParseIP(ip)); ok {
				result.MAC = mac
			}
		}
		if result.MAC != "" {
			if h := network.BorrowHostnameViaMAC(result.MAC); h != "" {
				result.Hostname = h
			}
		}
	}

	// Store the found hostname for other devices on the same MAC.
	if result.MAC != "" && result.Hostname != "" {
		network.StoreHostnameForMAC(result.MAC, result.Hostname)
	}

	return result.Hostname != initialHostname || result.MAC != initialMAC
}
