package testredis

import (
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/delayproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier of Far: what Far refuses, proved with no socket (2026-10-17,
// Glenn: no real sockets in unit tests, ever). A listener it is handed owns no
// port. The tests whose subject is the socket -- the proxy's own loopback
// listener, an echo server standing for the store, and the clients -- are in
// far_socket_functional_test.go, and the distance itself, through a real
// redis-server, is in far_functional_test.go.

// farDelay is the distance the tests ask for. With the fake clock it is a
// number the proxy is told and never a time that passes.
const farDelay = 100 * time.Millisecond

// farPipelined is how many of them the pipeline holds, and farSeparate is how
// many commands the test sends one after the other, each waiting for its reply.
const (
	farPipelined = 100
	farSeparate  = 3
)

// unbound stands in for net.Listen in the tests of what Far refuses: the
// listener it hands out owns no port, so a refusal made after the listener is
// opened is proved without a socket.
func unbound(string, string) (net.Listener, error) { return portless{}, nil }

// portless is a listener that owns no port: nothing is ever accepted on it.
type portless struct{}

func (portless) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (portless) Close() error              { return nil }
func (portless) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// Far is a way to make a local store far and never a way out of the machine,
// and it fails the test that asks for anything it cannot serve, naming why.
func TestFarFailsTheTestThatAsksForWhatItCannotServe(t *testing.T) {
	t.Parallel()

	noPort := func(string, string) (net.Listener, error) { return nil, errors.New("no descriptors left") }
	for name, c := range map[string]struct {
		target string
		delay  time.Duration
		listen func(network, address string) (net.Listener, error)
		want   string
	}{
		"a store off the machine":     {"example.com:80", farDelay, unbound, "not one"},
		"a target with no port":       {"127.0.0.1", farDelay, unbound, "not one"},
		"a negative delay":            {"127.0.0.1:7000", -time.Millisecond, unbound, "delay"},
		"a delay past the longest":    {"127.0.0.1:7000", delayproxy.MaxDelay + time.Nanosecond, unbound, "delay"},
		"a machine with no port left": {"127.0.0.1:7000", farDelay, noPort, "no descriptors left"},
	} {
		r := provoke(t, func(tb testing.TB) { real.far(tb, c.target, c.delay, delayproxy.Options{}, c.listen) })
		if !strings.Contains(r.fatal, c.want) {
			assert.Contains(t, r.fatal, c.want, "%s: failed with %q; want it to say %q", name, r.fatal, c.want)
		}
	}
	var listened atomic.Int64
	counting := func(network, address string) (net.Listener, error) {
		listened.Add(1)
		return unbound(network, address)
	}
	provoke(t, func(tb testing.TB) { real.far(tb, "example.com:80", farDelay, delayproxy.Options{}, counting) })
	if listened.Load() != 0 {
		assert.Fail(t, "a target off the machine was refused after a listener was opened; want it refused first")
	}
}

// Like the server Start runs, Far is for a test binary and nothing else.
func TestFarRefusesToRunOutsideATestBinary(t *testing.T) {
	t.Parallel()

	l := real
	l.inTest = func() bool { return false }
	said := panics(func() { l.far(t, "127.0.0.1:7000", farDelay, delayproxy.Options{}, unbound) })
	if s, _ := said.(string); !strings.Contains(s, "outside a test binary") {
		require.Contains(t, s, "outside a test binary", "Far outside a test binary panicked with %v; want the refusal", said)
	}
}
