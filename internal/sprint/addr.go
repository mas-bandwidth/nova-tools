package sprint

import (
	"net"
	"os"
	"strings"
)

// LocalOnlyMode returns true if NOVA_SPRINT_LOCAL is set to "1".
// When in local-only mode, only loopback addresses are accepted.
func LocalOnlyMode() bool {
	return os.Getenv("NOVA_SPRINT_LOCAL") == "1"
}

// AddrOK validates an address. It returns an empty string if the address is acceptable,
// or a reason string explaining why it's rejected.
func AddrOK(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "invalid address: " + addr
	}

	// If in local-only mode, only loopback is accepted.
	if LocalOnlyMode() {
		if isLoopback(host) {
			return ""
		}
		return "local-only mode allows only loopback; " + addr + " is not loopback"
	}

	// Normal mode: accept loopback, private, or tailnet.
	if isLoopback(host) || isPrivateOrTailnet(host) {
		return ""
	}
	return "address is neither loopback nor private nor tailnet: " + addr
}

// isLoopback reports whether the host is a loopback address.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isPrivateOrTailnet reports whether the host is a private address or tailnet range.
func isPrivateOrTailnet(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// Private ranges: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
	if ip.IsPrivate() {
		return true
	}
	// Tailnet: 100.64.0.0/10 (RFC 6598 shared address space)
	return ip.Equal(net.IPv4(100, 64, 0, 1)) || (ip[0] == 100 && ip[1] >= 64 && ip[1] < 128)
}

// contains reports whether xs contains x.
func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
