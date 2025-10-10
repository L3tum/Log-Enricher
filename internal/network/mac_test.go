package network

import (
	"net"
	"testing"
)

func TestMacFromEUI64(t *testing.T) {
	testCases := []struct {
		name      string
		ip        string
		expectMac string
		expectOk  bool
	}{
		{
			name:      "Valid EUI-64",
			ip:        "fe80::21a:11ff:fe12:1111",
			expectMac: "00:1a:11:12:11:11",
			expectOk:  true,
		},
		{
			name:      "Another Valid EUI-64",
			ip:        "fe80::5054:ff:fe12:3456",
			expectMac: "52:54:00:12:34:56",
			expectOk:  true,
		},
		{
			name:      "Non-EUI-64 IPv6",
			ip:        "2001:db8::1",
			expectMac: "",
			expectOk:  false,
		},
		{
			name:      "IPv4 Address",
			ip:        "192.168.1.1",
			expectMac: "",
			expectOk:  false,
		},
		{
			name:      "Nil IP",
			ip:        "",
			expectMac: "",
			expectOk:  false,
		},
		{
			name:      "Invalid IP string",
			ip:        "not-an-ip",
			expectMac: "",
			expectOk:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var ip net.IP
			if tc.ip != "" {
				ip = net.ParseIP(tc.ip)
			}

			mac, ok := MacFromEUI64(ip)

			if ok != tc.expectOk {
				t.Errorf("expected ok to be %v, but got %v", tc.expectOk, ok)
			}
			if mac != tc.expectMac {
				t.Errorf("expected mac to be %q, but got %q", tc.expectMac, mac)
			}
		})
	}
}
