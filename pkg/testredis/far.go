package testredis

import (
	"net"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/delayproxy"
)

// Far puts a store at a distance. It listens on 127.0.0.1, on a port the
// kernel chose, and forwards every connection to target, holding each write
// of the client back by delay before it forwards it. A client that dials the
// address it returns reaches the target as if the target stood a delay away:
// the answer is not held, so one command and its reply cost delay once, and a
// delay of 128ms is a store 128ms away by round trip. It is how a test judges
// a limit at real distance: a PING through Far(t, store, 100*time.Millisecond)
// costs one delay, a pipeline of a hundred written in one go costs one, and
// three commands sent one after the other cost three.
//
// A write is what one read of the client's connection returns, which for a
// write of up to 16 KiB on the loopback is the write. Reads that arrive
// together are each sent on at their own arrival plus delay, so they pay it
// once between them, up to a window of delayproxy.WindowBytes (4 MiB) in flight:
// a pipeline past it pays the delay once more for each further window.
//
// The delay is held by a timer, so a test that asserts on wall time asserts on
// its machine's load: a test of another package asserts on FarLink's ledger
// instead. The wall-clock windows of Far itself (one PING takes the delay, a
// pipeline takes it once, three commands one after the other take it three
// times) are asserted in the container by tools/fardelay's functional test, with
// a slack for a busy machine on the upper bounds.
//
// target is the address of a store this test owns, an IP in 127.0.0.0/8 or ::1
// or localhost, and a port, typically the address Start returned: Far is a way
// to make a local store far and never a way out of the machine, and it fails
// the test for any other target. It stops in the test's cleanup, with nothing
// it started left running. See FarLink for the proxy in hand.
func Far(t testing.TB, target string, delay time.Duration) (addr string) {
	t.Helper()
	return FarLink(t, target, delay).Addr()
}

// FarLink is Far with the proxy in hand, for a test that wants the ledger
// beside the address, as Start has StartServer. The proxy counts every write
// it has held and forwarded (Writes) and the least time any of them was held
// by its own clock (Shortest), so a test asserts the events: a pipeline of a
// hundred paid the delay once, three commands paid it three times and none was
// held less than the delay, and no test measures a wall clock a busy machine
// moves. Stop ends it before the test does.
func FarLink(t testing.TB, target string, delay time.Duration) *delayproxy.Proxy {
	t.Helper()
	return real.far(t, target, delay, delayproxy.Options{}, net.Listen)
}

// far is FarLink with everything it takes from outside itself, so a test can
// stand in for the listener and the clock.
func (l launch) far(t testing.TB, target string, delay time.Duration, opts delayproxy.Options, listen func(network, address string) (net.Listener, error)) *delayproxy.Proxy {
	t.Helper()
	l.refuse()
	if err := delayproxy.Loopback(target); err != nil {
		t.Fatalf("testredis: Far forwards to a store of this test's own on the loopback, and %q is not one: %v", target, err)
	}
	if opts.Logf == nil {
		opts.Logf = t.Logf
	}
	ln, err := listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testredis: the far proxy has no loopback port: %v", err)
	}
	p, err := delayproxy.Serve(ln, target, delay, opts)
	if err != nil {
		// ignored: a close on the failure path; t.Fatalf on the next line reports the Serve error
		_ = ln.Close()
		t.Fatalf("testredis: Far(%q, %v): %v", target, delay, err)
	}
	t.Cleanup(p.Stop)
	return p
}
