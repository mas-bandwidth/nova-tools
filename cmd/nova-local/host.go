package main

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// The serving host (docs/SPEC-LOCAL.md, rule 3 and "Fleet"): an engine is reached on
// loopback or over the tailnet, never anywhere else.

// tailnet is the address range a tailnet hands its machines (RFC 6598's shared range).
var tailnet = netip.MustParsePrefix("100.64.0.0/10")

// ServingHost is why host may not serve or be served from, "" when it may: loopback, or
// an address in the tailnet's range. A name is looked up and every address it resolves to
// must be one of those, so a name cannot point a local tier at a remote endpoint (rule 3).
func ServingHost(host string, lookup func(string) ([]string, error)) string {
	addrs := []string{host}
	if _, err := netip.ParseAddr(host); err != nil && !strings.EqualFold(host, "localhost") {
		var lerr error
		if addrs, lerr = lookup(host); lerr != nil || len(addrs) == 0 {
			return fmt.Sprintf("the host %s does not resolve here, so it cannot be checked to be loopback or the tailnet's", host)
		}
	}
	for _, a := range addrs {
		if strings.EqualFold(a, "localhost") {
			continue
		}
		ip, err := netip.ParseAddr(a)
		if err != nil || !(ip.IsLoopback() || tailnet.Contains(ip.Unmap())) {
			return fmt.Sprintf("the host %s is %s, neither loopback nor the tailnet's (100.64.0.0/10); a local tier pointed at a remote endpoint is not a local tier", host, a)
		}
	}
	return ""
}

// BaseProblem is why base cannot be an engine's base URL, "" when it can: an http URL
// whose host may serve (ServingHost).
func BaseProblem(base string, lookup func(string) ([]string, error)) string {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" {
		return fmt.Sprintf("--base wants an http URL of an engine's /v1, such as http://127.0.0.1:%s/v1 (got %q)", OllamaPort, base)
	}
	if p := ServingHost(u.Hostname(), lookup); p != "" {
		return "--base " + base + ": " + p
	}
	return ""
}
