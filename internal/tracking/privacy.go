package tracking

import (
	"net"
	"net/netip"
)

// TruncateIP masks IPv4 to /24 and IPv6 to /48 (S11 D11).
func TruncateIP(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	if ip.Is4() {
		a := ip.As4()
		return net.IPv4(a[0], a[1], a[2], 0).String()
	}
	if ip.Is6() {
		a := ip.As16()
		for i := 6; i < 16; i++ {
			a[i] = 0
		}
		return netip.AddrFrom16(a).String()
	}
	return ip.String()
}
