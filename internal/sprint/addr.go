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

// CheckAddr is why addr is not an address nova-sprint dials (its store), ""
// when it is; the bind side of the same rule is ListenRefused. Outside
// local-only mode loopback, a private range or the
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
		return LocalOnlyRefusal(ip, addr)
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

// LocalOnlyRefusal is the one sentence every address rule prints when
// local-only mode refuses an address: the rule, the address as given, and the
// mode as the reason. ip is the address's parsed host; nil (a name, or
// localhost) is not refused here, each caller having judged the name already,
// and localhost binds loopback. "" when ip is loopback, or nil
// (docs/SPEC-SPRINT.md, section 14, The server).
func LocalOnlyRefusal(ip net.IP, addr string) string {
	if ip == nil || ip.IsLoopback() {
		return ""
	}
	return "local-only mode allows only loopback; " + addr + " is not loopback"
}

// ListenRefused is why the IP address ip is not an address nova-sprint binds
// (the server's --listen, the dashboard's --listen and --pull), "" when it is:
// the listen side of the one address rule, kept in addr.go beside CheckAddr
// and cited from each caller, so the dial side and the bind side decide on the
// same ranges. A name is refused (a listener binds one address of this
// machine), and so are every network, a link-local address and a public
// address: the server and the page check no credential, so they listen on
// loopback, a private range or the tailnet's address only. Local-only mode
// narrows the accepted binds to loopback and localhost; that refusal is
// LocalOnlyRefusal, cited from each caller, so the mode needs no tailnet on
// the listen side either (docs/SPEC-SPRINT.md, section 14, The server).
func ListenRefused(ip net.IP) string {
	switch {
	case ip == nil:
		return "the address is an IP address of this machine (loopback, or its address on the fleet's private network), never a name"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return "a link-local address; the page checks no credential, so it listens on loopback or the fleet's private network (the tailnet) only"
	case ip.IsUnspecified():
		return "the page shows the sprint and checks no credential, so it does not listen on every network; name loopback or this machine's tailnet address"
	case !ip.IsLoopback() && !ip.IsPrivate() && !tailnetRange.Contains(ip):
		return "a public address; the page shows the sprint and checks no credential, so it listens on loopback or the fleet's private network (the tailnet) only"
	}
	return ""
}
