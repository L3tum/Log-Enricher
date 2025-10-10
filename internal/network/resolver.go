package network

import (
	"context"
	"log"
	"net"
	"strings"
	"time"

	"log-enricher/internal/config"
)

func CustomResolver(cfg *config.Config) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", cfg.DNSServer)
		},
	}
}

func ResolveRDNS(ip string, resolver *net.Resolver) string {
	names, err := resolver.LookupAddr(context.Background(), ip)
	if err != nil || len(names) == 0 {
		log.Printf("rDNS lookup failed for %s: %v", ip, err)
		return ""
	}
	host := strings.TrimSuffix(names[0], ".")
	log.Printf("rDNS lookup success: %s -> %s", ip, host)
	return host
}
