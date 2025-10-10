package network

import (
	"net"
)

// MacFromEUI64 derives MAC from IPv6 SLAAC EUI-64 address if possible
func MacFromEUI64(ip net.IP) (string, bool) {
	if ip == nil || ip.To4() != nil {
		return "", false
	}
	b := ip.To16()
	if b == nil {
		return "", false
	}
	iid := b[8:]
	if len(iid) != 8 {
		return "", false
	}
	// Typical EUI-64 has ff:fe inserted
	if !(iid[3] == 0xff && iid[4] == 0xfe) {
		return "", false
	}
	mac := []byte{
		iid[0] ^ 0x02,
		iid[1],
		iid[2],
		iid[5],
		iid[6],
		iid[7],
	}
	return net.HardwareAddr(mac).String(), true
}
