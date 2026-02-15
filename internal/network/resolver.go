package network

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"
)

func CustomResolver(dnsServer string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", dnsServer)
		},
	}
}

func ResolveRDNS(ctx context.Context, ip string, resolver *net.Resolver) string {
	names, err := resolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	host := strings.TrimSuffix(names[0], ".")
	slog.Info("rDNS lookup success", "ip", ip, "host", host)
	return host
}
