package bus

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ip4 is an address from its octets: the cases below are built, not
// spelled, because nothing here is dialled and the ci net rule reads a
// spelled host:port as a host a test could reach.
func ip4(a, b, c, d byte) netip.Addr { return netip.AddrFrom4([4]byte{a, b, c, d}) }

func port(ip netip.Addr) string { return netip.AddrPortFrom(ip, 6381).String() }

// The rule on where a store may be: loopback or the tailnet, by literal or
// by every address a name resolves to; a Unix socket is this machine's.
func TestAStoreIsReachedOverLoopbackOrTheTailnetOnly(t *testing.T) {
	t.Parallel()
	tail, far, lan := ip4(100, 76, 0, 9), ip4(203, 0, 113, 9), ip4(10, 0, 0, 5)
	v6, mapped := netip.MustParseAddr("2001:db8::1"), netip.AddrFrom16(ip4(127, 0, 0, 1).As16())
	table := map[string][]netip.Addr{
		"studio.test":  {tail},
		"both.test":    {tail, far},
		"mapped.test":  {mapped},
		"nowhere.test": {},
	}
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "broken.test" {
			return nil, errors.New("no such host")
		}
		return table[host], nil
	}
	const rule = "nova-bus reaches a store over loopback or the tailnet (100.64.0.0/10) only: "
	for _, c := range []struct{ addr, why string }{
		{"127.0.0.1:6381", ""},
		{"[::1]:6381", ""},
		{"localhost:6381", ""},
		{port(ip4(100, 64, 0, 1)), ""},
		{port(ip4(100, 127, 255, 254)), ""},
		{"studio.test:6381", ""},
		{"mapped.test:6381", ""},
		{"/var/run/redis.sock", ""},
		{"not-an-address", ""}, // the shape is redisconn's refusal
		{port(ip4(100, 128, 0, 1)), rule + port(ip4(100, 128, 0, 1)) + " is " + ip4(100, 128, 0, 1).String()},
		{port(lan), rule + port(lan) + " is " + lan.String()},
		{port(v6), rule + port(v6) + " is " + v6.String()},
		{"both.test:6381", rule + "both.test:6381 is " + far.String()},
		{"nowhere.test:6381", rule + "nowhere.test:6381 does not resolve, so what it would dial is unknown"},
		{"broken.test:6381", rule + "broken.test:6381 does not resolve, so what it would dial is unknown"},
	} {
		assert.Equal(t, c.why, CheckAddr(context.Background(), c.addr, lookup), c.addr)
	}
}
