// Package sprint implements the sprint table. addr.go provides address validation
// for local-only mode and tailnet addresses.
package sprint

import (
	"net"
	"os"
)

// LocalOnlyMode says whether the sprint is running in local-only mode, where
// no tailnet is needed and only loopback addresses are accepted. It is true
// when the sprint row has local-only set or NOVA_SPRINT_LOCAL=1 is set.
func LocalOnlyMode() bool {
	return os.Getenv("NOVA_SPRINT_LOCAL") == "1"
}

// CheckAddr validates an address for the sprint. In local-only mode, only
// loopback addresses are accepted. Otherwise, loopback or tailnet addresses
// are accepted. Returns "" when ok, else a refusal describing the mode and
// why the address was refused.
func CheckAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		why := "address wants host:port"
		if host == "" {
			why = "address is empty"
		}
		return why
	}

	if host == "" {
		return "host is empty"
	}

	ip := net.ParseIP(host)
	if ip == nil {
		if LocalOnlyMode() {
			return "local-only mode: address must be an IP (loopback only); found " + host
		}
		return "address must be an IP (loopback or tailnet); found " + host
	}

	if ip.IsLoopback() {
		return ""
	}

	if LocalOnlyMode() {
		return "local-only mode: only loopback addresses are accepted; found " + host
	}

	// Check if it's a tailnet address (100.64.0.0/10)
	tailnetRange := &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	if tailnetRange.Contains(ip) {
		return ""
	}

	return "address must be loopback or tailnet; found " + host
}

// IsLoopback says whether the address is a loopback address.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsTailnet says whether the address is a tailnet address.
func IsTailnet(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	tailnetRange := &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return tailnetRange.Contains(ip)
}

// AcceptableAddr says whether the address is acceptable given the current mode.
func AcceptableAddr(addr string) bool {
	return CheckAddr(addr) == ""
}

// AddrRefusal returns the refusal message for the address, or empty if acceptable.
func AddrRefusal(addr string) string {
	return CheckAddr(addr)
}
