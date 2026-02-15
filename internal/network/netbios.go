package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

var netbiosQueryPacket = []byte{
	0x00, 0x00, // Transaction ID
	0x00, 0x10, // Flags: Standard query
	0x00, 0x01, // Questions: 1
	0x00, 0x00, // Answer RRs: 0
	0x00, 0x00, // Authority RRs: 0
	0x00, 0x00, // Additional RRs: 0
	// Query for "*" (wildcard)
	0x20, 0x43, 0x4b, 0x41, 0x41, 0x41, 0x41, 0x41,
	0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41,
	0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41,
	0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41,
	0x41, 0x00,
	0x00, 0x21, // Type: NB (NetBIOS name service)
	0x00, 0x01, // Class: IN
}

// QueryNetBIOSName attempts to get the NetBIOS name from a Windows device
func QueryNetBIOSName(parentCtx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(parentCtx, 2*time.Second)
	defer cancel()

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "udp", fmt.Sprintf("%s:137", ip))
	if err != nil {
		return ""
	}
	defer conn.Close()

	if _, err := conn.Write(netbiosQueryPacket); err != nil {
		return ""
	}

	response := make([]byte, 512)
	n, err := conn.Read(response)
	if err != nil || n < 56 {
		return ""
	}

	// Parse the NetBIOS name from the response
	if hostname := parseNetBIOSResponse(response[:n]); hostname != "" {
		log.Printf("NetBIOS lookup success: %s -> %s", ip, hostname)
		return hostname
	}

	return ""
}

func parseNetBIOSResponse(data []byte) string {
	if len(data) < 56 {
		return ""
	}

	// Check if it's a positive response
	if data[2]&0x80 == 0 || data[3]&0x0F != 0 {
		return ""
	}

	// Number of answers
	answers := binary.BigEndian.Uint16(data[6:8])
	if answers == 0 {
		return ""
	}

	// Skip to the answer section (starts at byte 56 typically)
	offset := 56
	if len(data) < offset+6 {
		return ""
	}

	// Number of names in the response
	numNames := int(data[offset])
	offset++

	for i := 0; i < numNames && offset+18 <= len(data); i++ {
		nameBytes := data[offset : offset+15]
		nameType := data[offset+15]

		// Type 0x00 or 0x20 indicates a workstation/server name
		if nameType == 0x00 || nameType == 0x20 {
			name := strings.TrimSpace(string(nameBytes))
			if name != "" && !strings.Contains(name, "\x00") {
				return name
			}
		}

		offset += 18
	}

	return ""
}
