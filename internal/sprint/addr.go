// sprint package address validation
package sprint

import (
	"net"
	"os"
	"strings"
)

// tailnetRange is the addresses a tailnet hands out (100.64.0.0/10, the shared range).
var tailnetRange = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

// LocalOnlyMode returns true when the sprint is in local-only mode.
// Local-only mode is enabled by setting NOVA_SPRINT_LOCAL=1.
// In this mode, only loopback addresses are allowed; tailnet addresses are refused.
func LocalOnlyMode() bool {
	return strings.ToLower(os.Getenv("NOVA_SPRINT_LOCAL")) == "1"
}

// AddrOK returns an empty string when addr is acceptable, or a refusal message otherwise.
// Acceptable addresses are:
//   - Loopback addresses (127.0.0.0/8)
//   - Private addresses (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
//   - Tailnet addresses (100.64.0.0/10)
//
// When in local-only mode, only loopback is acceptable.
func AddrOK(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "address wants host:port, found " + addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "address is an IP, not a name"
	}

	if LocalOnlyMode() {
		if !ip.IsLoopback() {
			return "local-only mode allows only loopback; " + addr + " is not loopback"
		}
		return ""
	}

	if !ip.IsLoopback() && !ip.IsPrivate() && !tailnetRange.Contains(ip) {
		return "address is neither loopback nor private nor tailnet: " + addr
	}
	return ""
}

// IsLoopback returns true when ip is a loopback address.
func IsLoopback(ip net.IP) bool {
	return ip != nil && ip.IsLoopback()
}

// IsPrivateOrTailnet returns true when ip is a private or tailnet address.
func IsPrivateOrTailnet(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || tailnetRange.Contains(ip)
}
