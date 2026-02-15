package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"log-enricher/internal/cache"
	"log-enricher/internal/models"
	"log-enricher/internal/network"

	"github.com/mitchellh/mapstructure"
)

// HostnameConfig holds the configuration for the hostname enrichment stage.
type HostnameConfig struct {
	ClientIpField       string `mapstructure:"client_ip_field"`
	ClientHostnameField string `mapstructure:"client_hostname_field"`
	DNSServer           string `mapstructure:"dns_server"`
	EnableRDNS          bool   `mapstructure:"enable_rdns"`
	EnableMDNS          bool   `mapstructure:"enable_mdns"`
	EnableLLMNR         bool   `mapstructure:"enable_llmnr"`
	EnableNetBIOS       bool   `mapstructure:"enable_netbios"`
}

type HostnameEnrichmentStage struct {
	cfg                *HostnameConfig
	macToHostnameCache *cache.PersistedCache[string]
	ipToMacCache       *cache.PersistedCache[string]
	resolver           *net.Resolver
	ctx                context.Context
}

func NewHostnameEnrichmentStage(ctx context.Context, params map[string]any) (Stage, error) {
	var stageConfig HostnameConfig
	if err := mapstructure.WeakDecode(params, &stageConfig); err != nil {
		return nil, fmt.Errorf("failed to decode hostname enrichment stage config: %w", err)
	}

	if stageConfig.ClientIpField == "" {
		stageConfig.ClientIpField = "client_ip"
	}

	if stageConfig.ClientHostnameField == "" {
		stageConfig.ClientHostnameField = "client_hostname"
	}

	slog.Info("Initializing hostname enrichment stage.")
	slog.Info("DNS server", "server", stageConfig.DNSServer)
	slog.Info("Client IP field", "field", stageConfig.ClientIpField)
	slog.Info("Client hostname field", "field", stageConfig.ClientHostnameField)
	slog.Info("RDNS", "enabled", stageConfig.EnableRDNS)
	slog.Info("MDNS", "enabled", stageConfig.EnableMDNS)
	slog.Info("LLMNR", "enabled", stageConfig.EnableLLMNR)
	slog.Info("NetBIOS", "enabled", stageConfig.EnableNetBIOS)

	stage := &HostnameEnrichmentStage{
		cfg:                &stageConfig,
		macToHostnameCache: cache.NewPersistedCache[string]("mac_to_hostname", 10, 100, true),
		ipToMacCache:       cache.NewPersistedCache[string]("ip_to_mac", 10, 100, true),
		resolver:           network.CustomResolver(stageConfig.DNSServer),
		ctx:                ctx,
	}

	// Start the neighbor discovery & cache refresh goroutines
	go network.WatchNeighbors(ctx)
	go stage.refresh()

	return stage, nil
}

func (h *HostnameEnrichmentStage) Name() string {
	return "hostname enrichment"
}

func (h *HostnameEnrichmentStage) Process(entry *models.LogEntry) (keep bool, err error) {
	clientIp, ok := entry.Fields[h.cfg.ClientIpField]

	if !ok {
		return true, nil
	}

	clientIP, ok := clientIp.(string)

	if !ok {
		return true, nil
	}

	result := models.Result{}

	// Get MAC from cache
	if mac, ok := h.ipToMacCache.Get(clientIP); ok {
		h.ipToMacCache.Hit()
		result.MAC = mac
	} else {
		h.ipToMacCache.Miss()
	}

	// TODO: Doesn't do anything? (Cache is empty)
	// Try to get Hostname
	if hostname, ok := h.macToHostnameCache.Get(result.MAC); ok {
		h.macToHostnameCache.Hit()
		entry.Fields[h.cfg.ClientHostnameField] = hostname
	} else {
		h.macToHostnameCache.Miss()

		if h.performEnrichment(clientIP, &result) {
			h.macToHostnameCache.Set(result.MAC, result.Hostname)
			h.ipToMacCache.Set(clientIP, result.MAC)
			entry.Fields[h.cfg.ClientHostnameField] = result.Hostname
		}
	}

	return true, nil
}

func (h *HostnameEnrichmentStage) performEnrichment(clientIP string, result *models.Result) bool {
	updated := false
	isIPv6 := strings.Contains(clientIP, ":")

	// Fetch MAC/Hostname from NDP (Neighbour Discovery Protocol) if we don't have it yet
	if result.MAC == "" {
		if mac := network.GetMACForIP(clientIP); mac != "" {
			result.MAC = mac
			updated = true
		} else {
			network.TriggerNeighborDiscovery(clientIP)

			if mac := network.GetMACForIP(clientIP); mac != "" {
				result.MAC = mac
				updated = true
			}
		}
	}

	// Fetch hostname from NDP if we don't have it yet
	if result.MAC != "" {
		if hostname := network.BorrowHostnameViaMAC(result.MAC); hostname != "" {
			result.Hostname = hostname
			updated = true
		}
	}

	// Do an active discovery if we don't have a hostname or MAC yet
	if result.Hostname == "" {
		if hostname := h.doActiveDiscovery(clientIP, isIPv6); hostname != "" {
			result.Hostname = hostname
			updated = true
		}
	}

	// --- Post-lookup MAC-based logic (if no hostname was found) ---
	if result.Hostname == "" {
		if isIPv6 && result.MAC == "" {
			if mac, ok := network.MacFromEUI64(net.ParseIP(clientIP)); ok {
				result.MAC = mac
				updated = true
			}
		}
	}

	// Store the found hostname for other devices on the same MAC.
	if result.MAC != "" && result.Hostname != "" {
		network.StoreHostnameForMAC(result.MAC, result.Hostname)
	}

	return updated
}

func (h *HostnameEnrichmentStage) doActiveDiscovery(ip string, isIPv6 bool) string {
	// context.WithCancel for early termination.
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel() // Ensure context resources are always released

	var wg sync.WaitGroup
	var once sync.Once
	foundHost := make(chan string, 1)         // Buffered channel to store the first found hostname
	allLookupsFinished := make(chan struct{}) // Channel to signal when all lookups have finished

	// --- Define Lookup Functions ---
	runLookup := func(lookupFunc func(context context.Context) string) {
		defer wg.Done()
		host := lookupFunc(ctx)
		if host != "" {
			// Use sync.Once to ensure we only attempt to send the first result.
			// This also implicitly means cancel() is called only once by a successful lookup.
			once.Do(func() {
				select {
				case <-ctx.Done():
					// Context was already cancelled by another lookup that found a host.
					// Do not send this result, as a host has already been found and processed.
					return
				case foundHost <- host:
					// Successfully sent the host. Now, cancel other ongoing lookups
					// to signal them to stop processing or sending their results.
					cancel()
				}
			})
		}
	}

	// --- Launch Lookups in Parallel ---
	if h.cfg.EnableRDNS {
		wg.Add(1)
		go runLookup(func(ctx context.Context) string { return network.ResolveRDNS(ctx, ip, h.resolver) })
	}
	if h.cfg.EnableMDNS {
		wg.Add(1)
		go runLookup(func(ctx context.Context) string { return network.QueryMDNS(ctx, ip) })
	}
	if !isIPv6 && h.cfg.EnableNetBIOS {
		wg.Add(1)
		go runLookup(func(ctx context.Context) string { return network.QueryNetBIOSName(ctx, ip) })
	}
	if h.cfg.EnableLLMNR {
		wg.Add(1)
		go runLookup(func(ctx context.Context) string { return network.QueryLLMNR(ctx, ip) })
	}

	// Background wg.Wait() to not block the main Run method.
	// This goroutine waits for all lookup goroutines to complete and then signals.
	go func() {
		wg.Wait()
		close(allLookupsFinished)
	}()

	// Step 4: Main select logic to wait for a result or for all lookups to finish.
	select {
	case host := <-foundHost:
		// A host was found and successfully sent to the channel.
		// The 'cancel()' function was already called by the runLookup goroutine.
		return host
		// The main Run method can now proceed with post-lookup logic.
		// The background goroutine will continue to wait for all lookups to finish.
	case <-allLookupsFinished:
		// All lookup goroutines have completed, and no host was sent to foundHost
		// (or if it was, the 'foundHost' case was not selected first).
		// Perform a final non-blocking check on foundHost in case a result was sent
		// just before allLookupsFinished was closed.
		select {
		case host := <-foundHost:
			return host
		default:
			// No hostname was found by any of the enabled lookups, or all lookups failed.
		}
	}

	return ""
}

func (h *HostnameEnrichmentStage) refresh() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			{
				// Copy the cache to avoid concurrent modification errors.
				ipsToMacs := h.ipToMacCache.Copy()
				macsToHostnames := h.macToHostnameCache.Copy()

				// Refresh the cache
				for ip, mac := range ipsToMacs {
					// Check if the MAC is still in the cache (i.e. the two caches diverged)
					if _, ok := macsToHostnames[mac]; !ok {
						h.ipToMacCache.Delete(ip)
						continue
					}

					// Perform a lookup for the IP
					newResult := &models.Result{}
					if h.performEnrichment(ip, newResult) {
						h.ipToMacCache.Set(ip, newResult.MAC)
						h.macToHostnameCache.Set(newResult.MAC, newResult.Hostname)
					}
				}

				// Remove any MACs that are no longer in the IP cache
				for _, mac := range h.macToHostnameCache.Keys() {
					found := false
					for _, ipedMac := range ipsToMacs {
						if ipedMac == mac {
							found = true
							break
						}
					}

					if !found {
						h.macToHostnameCache.Delete(mac)
					}
				}
			}
		}
	}
}
