package network

import (
	"testing"
)

// This file is for testing functions in mdns.go

func Test_reverseAddr(t *testing.T) {
	testCases := []struct {
		name        string
		ip          string
		expected    string
		expectError bool
	}{
		{
			name:        "Valid IPv4",
			ip:          "8.8.4.4",
			expected:    "4.4.8.8.in-addr.arpa.",
			expectError: false,
		},
		{
			name:        "Valid IPv6",
			ip:          "2001:4860:4860::8888",
			expected:    "8.8.8.8.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.6.8.4.0.6.8.4.1.0.0.2.ip6.arpa.",
			expectError: false,
		},
		{
			name:        "Invalid IP",
			ip:          "not-an-ip",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Empty IP",
			ip:          "",
			expected:    "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reversed, err := reverseAddr(tc.ip)

			if (err != nil) != tc.expectError {
				t.Fatalf("expected error: %v, but got: %v", tc.expectError, err)
			}

			if reversed != tc.expected {
				t.Errorf("expected reversed address %q, but got %q", tc.expected, reversed)
			}
		})
	}
}
