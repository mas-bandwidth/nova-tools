package sprint

import (
	"net"
	"os"
	"strings"
)

// tailnetRange is the one network beyond loopback nova-sprint's store is
// reached over: the shared range RFC 6598 reserves for carrier-grade NAT, which
// Tailscale uses (100.64.0.0/10). The store address is loopback or an address in
// it, and on nothing else, the way nova-bus's rule on its store is
// (internal/bus/addr.go, Tailnet). The rule is one function, CheckAddr, cited
// from each caller (docs/SPEC-SPRINT.md, sprint-local-only-mode-r-bcb.w5).
var tailnetRange = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

// IsTailnet reports whether ip is an address of the tailnet (100.64.0.0/10).
// The dashboard's wording for its listener ranges cites it, so the range lives
// in one place (internal/sprint/addr.go).
func IsTailnet(ip net.IP) bool { return ip != nil && tailnetRange.Contains(ip) }

// LocalOnlyModeFrom reports whether getenv selects local-only mode
// (NOVA_SPRINT_LOCAL=1). The mode is a process setting, not a sprint row: a row
// needs a store to be read, and the single machine the mode is for may have no
// store yet, so the process names the mode before any store exists
// (docs/SPEC-SPRINT.md, sprint-local-only-mode-r-bcb.w5). A check reads it
// through its Env, never the process directly.
func LocalOnlyModeFrom(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(getenv("NOVA_SPRINT_LOCAL")), "1")
}

// LocalOnlyMode reports whether this process runs in local-only mode
// (LocalOnlyModeFrom over the process environment). In this mode only loopback
// addresses are allowed; a tailnet or other address is refused naming the mode,
// and nothing asks for a tailnet.
func LocalOnlyMode() bool { return LocalOnlyModeFrom(os.Getenv) }

// CheckAddr is why addr is not an address nova-sprint dials or listens on, ""
// when it is. Loopback and the tailnet (100.64.0.0/10) are the only addresses
// accepted; every other address is refused before a socket is opened. An
// address with no host (`:6379`) is refused too: it names every interface of
// this machine, not loopback, and nothing here binds every interface, so under
// local-only mode that refusal names the mode too. A name
// that is no IP literal, a private address outside the tailnet, a link-local
// address and an unspecified address are all refused, each naming what the
// address is. In local-only mode every address must be loopback and a tailnet or
// other address is refused naming the mode. "mem", a mem: twin and a Unix socket
// path need no network and pass. The rule is one function, cited from each
// caller (docs/SPEC-SPRINT.md, sprint-local-only-mode-r-bcb.w5).
func CheckAddr(addr string, localOnly bool) string {
	if addr == "mem" || strings.HasPrefix(addr, "mem:") || strings.HasPrefix(addr, "/") {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "address wants host:port, found " + addr
	}
	if host == "" {
		if localOnly {
			return "local-only mode allows only loopback; " + addr + " names no host (loopback, 127.0.0.1, only)"
		}
		return "the address names no host: " + addr + " (loopback or the tailnet, 100.64.0.0/10, only)"
	}
	if host == "localhost" {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if localOnly {
			return "local-only mode allows only loopback; " + addr + " is not loopback: what a name would dial is unknown"
		}
		return "the address is an IP address of this machine (loopback, or its address on the fleet's tailnet), never a name"
	}
	if ip.IsUnspecified() {
		if localOnly {
			return "local-only mode allows only loopback; " + addr + " names this machine on every network, not loopback"
		}
		return "every network: " + addr + " names this machine on every network, not loopback or the tailnet (100.64.0.0/10)"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		if localOnly {
			return "local-only mode allows only loopback; " + addr + " is a link-local address, not loopback"
		}
		return "a link-local address: " + addr + " is not loopback or the tailnet (100.64.0.0/10)"
	}
	if ip.IsLoopback() {
		return ""
	}
	if localOnly {
		return "local-only mode allows only loopback; " + addr + " is not loopback"
	}
	if tailnetRange.Contains(ip) {
		return ""
	}
	if ip.IsPrivate() {
		return "a private address outside the tailnet: " + addr + " is neither loopback nor the tailnet (100.64.0.0/10)"
	}
	return "a public address: address is neither loopback nor tailnet (100.64.0.0/10): " + addr
}
