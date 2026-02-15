package network

import (
	"context"
	"log"
	"net"
	"strings"
	"time"
)

var llmnrResolver = &net.Resolver{
	PreferGo: true,
	Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		// The address from LookupAddr is "ip:port", so we extract the IP.
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address // Fallback for addresses without port
		}

		d := net.Dialer{Timeout: 2 * time.Second}
		// LLMNR uses 224.0.0.252:5355 for IPv4, ff02::1:3:5355 for IPv6
		llmnrAddr := "224.0.0.252:5355"
		if strings.Contains(host, ":") {
			llmnrAddr = "[ff02::1:3]:5355"
		}
		return d.DialContext(ctx, "udp", llmnrAddr)
	},
}

// QueryLLMNR attempts to resolve hostname via LLMNR (Link-Local Multicast Name Resolution)
func QueryLLMNR(parentCtx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(parentCtx, 2*time.Second)
	defer cancel()

	names, err := llmnrResolver.LookupAddr(ctx, ip)
	if err == nil && len(names) > 0 {
		hostname := strings.TrimSuffix(names[0], ".")
		log.Printf("LLMNR lookup success: %s -> %s", ip, hostname)
		return hostname
	}

	return ""
}
