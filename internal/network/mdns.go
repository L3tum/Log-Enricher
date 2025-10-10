package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

var mdnsResolver = &net.Resolver{
	PreferGo: true,
	Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		// The address from LookupAddr is "ip:port", so we extract the IP.
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address // Fallback for addresses without port
		}

		d := net.Dialer{Timeout: 2 * time.Second}
		// mDNS uses 224.0.0.251:5353 for IPv4, ff02::fb:5353 for IPv6
		mdnsAddr := "224.0.0.251:5353"
		if strings.Contains(host, ":") {
			mdnsAddr = "[ff02::fb]:5353"
		}
		return d.DialContext(ctx, "udp", mdnsAddr)
	},
}

// QueryMDNS attempts to resolve hostname via mDNS (multicast DNS)
func QueryMDNS(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	names, err := mdnsResolver.LookupAddr(ctx, ip)
	if err == nil && len(names) > 0 {
		hostname := strings.TrimSuffix(names[0], ".local.")
		hostname = strings.TrimSuffix(hostname, ".")
		log.Printf("mDNS lookup success: %s -> %s", ip, hostname)
		return hostname
	}

	return ""
}

func reverseAddr(ip string) (string, error) {
	addr := net.ParseIP(ip)
	if addr == nil {
		return "", fmt.Errorf("invalid IP address")
	}

	if addr.To4() != nil {
		// IPv4
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", addr[15], addr[14], addr[13], addr[12]), nil
	}

	// IPv6
	buf := make([]byte, 0, len(ip)*4+len("ip6.arpa."))
	for i := len(addr) - 1; i >= 0; i-- {
		v := addr[i]
		buf = append(buf, hexDigit[v&0xF])
		buf = append(buf, '.')
		buf = append(buf, hexDigit[v>>4])
		buf = append(buf, '.')
	}
	buf = append(buf, "ip6.arpa."...)
	return string(buf), nil
}

const hexDigit = "0123456789abcdef"
