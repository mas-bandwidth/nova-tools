package sprint

import (
	"net"
	"os"
	"strings"
)

// tailnetRange is the IPv4 address range reserved for tailnet addresses.
var tailnetRange = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

// LocalOnlyModeFrom reports whether getenv selects local-only mode
// (NOVA_SPRINT_LOCAL=1). The mode is a process setting, not a sprint row: a row
// needs a store to be read, and the single machine the mode is for may have no
// store yet, so the process says it (docs/SPEC-SPRINT.md, section 14, The server).
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
func LocalOnlyMode() bool {
	return LocalOnlyModeFrom(os.Getenv)
}

// CheckAddr is why addr is not an address nova-sprint dials or listens on, ""
// when it is. Outside local-only mode loopback, a private range or the
// tailnet's address in host:port form is accepted and anything else is refused
// before a socket is opened; in local-only mode every address must be loopback.
// "localhost", a Unix socket and a mem: twin need no network and pass. A name
// that is no IP literal passes outside the mode (what it would dial is the
// resolver's to say; the address shape is redisconn's to refuse) and is refused
// naming the mode inside it, since only loopback is known to stay on this
// machine (docs/SPEC-SPRINT.md, section 14, The server).
func CheckAddr(addr string, localOnly bool) string {
	if addr == "mem" || strings.HasPrefix(addr, "mem:") || strings.HasPrefix(addr, "/") {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "address wants host:port, found " + addr
	}
	if host == "" || host == "localhost" {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if localOnly {
			return "local-only mode allows only loopback; " + addr + " is not loopback: what a name would dial is unknown"
		}
		return ""
	}
	if ip.IsLoopback() {
		return ""
	}
	if localOnly {
		return "local-only mode allows only loopback; " + addr + " is not loopback"
	}
	if ip.IsPrivate() || tailnetRange.Contains(ip) {
		return ""
	}
	return "address is neither loopback nor private nor tailnet (100.64.0.0/10): " + addr
}

// AddrOK is CheckAddr in this process's mode: "" when addr is acceptable, else
// the refusal naming the rule, or the mode when it refuses.
func AddrOK(addr string) string {
	return CheckAddr(addr, LocalOnlyMode())
}
