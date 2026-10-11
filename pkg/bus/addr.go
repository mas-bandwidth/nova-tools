package bus

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Tailnet is the one network beyond loopback the bus reaches a store over:
// the tailnet is the boundary, with no ACL behind it (decided 2026-10-04),
// so an address outside it is refused before anything is dialled.
var Tailnet = netip.MustParsePrefix("100.64.0.0/10")

// Lookup resolves a host name to its addresses: net.DefaultResolver's
// LookupNetIP in the tool, a table in a test.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

// CheckAddr is the rule on where a store may be: "" when addr (host:port,
// or the absolute path of a Unix socket, which is this machine's) is on
// loopback or the tailnet, else the one line that names the rule and what
// the address is. A name is resolved and every address it has must pass; a
// name that does not resolve fails the rule too, since what it would dial is
// unknown. The shape of the address is redisconn's to refuse, not this.
func CheckAddr(ctx context.Context, addr string, lookup Lookup) string {
	if strings.HasPrefix(addr, "/") {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" || host == "localhost" {
		return ""
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else if ips, err = lookup(ctx, host); err != nil || len(ips) == 0 {
		return fmt.Sprintf("nova-bus reaches a store over loopback or the tailnet (100.64.0.0/10) only: %s does not resolve, so what it would dial is unknown", addr)
	}
	for _, ip := range ips {
		if ip = ip.Unmap(); !ip.IsLoopback() && !Tailnet.Contains(ip) {
			return fmt.Sprintf("nova-bus reaches a store over loopback or the tailnet (100.64.0.0/10) only: %s is %s", addr, ip)
		}
	}
	return ""
}
