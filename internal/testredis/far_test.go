package testredis

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/delayproxy"
)

// The unit tier of Far. Nothing here waits on a clock: the proxy is given a
// clock that writes its waits down, so "one delay for each write" is a count.
// The only sockets are loopback ones the tests own: the proxy's own listener,
// an echo server standing for the store, and the clients. The distance itself,
// through a real redis-server, is in far_functional_test.go.

// farDelay is the distance the tests ask for. With the fake clock it is a
// number the proxy is told and never a time that passes.
const farDelay = 100 * time.Millisecond

// farCeiling bounds every read of a socket here, generously: a test that is not
// answered fails at it and does not hang.
const farCeiling = 30 * time.Second

// farPing is one command of a pipeline, PING as a RESP array of 14 bytes.
const farPing = "*1\r\n$4\r\nPING\r\n"

// farPipelined is how many of them the pipeline holds, and farSeparate is how
// many commands the test sends one after the other, each waiting for its reply.
const (
	farPipelined = 100
	farSeparate  = 3
)

// farOnly reports whether the clock was asked for exactly n waits, each of the
// whole farDelay.
func farOnly(asked []time.Duration, n int) bool {
	if len(asked) != n {
		return false
	}
	for _, d := range asked {
		if d != farDelay {
			return false
		}
	}
	return true
}

// farEcho is a loopback server the test owns: it sends back every byte it reads
// and stops with the test.
func farEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var serving sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		serving.Wait()
	})
	serving.Add(1)
	go func() {
		defer serving.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			serving.Add(1)
			go func() {
				defer serving.Done()
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

// farClock stands still and writes down every wait it is asked for. Given a
// gate, a wait signals on waiting and blocks until the connection ends.
type farClock struct {
	mu      sync.Mutex
	waits   []time.Duration
	blocked chan struct{}
}

var farEpoch = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func (f *farClock) options() delayproxy.Options {
	return delayproxy.Options{Clock: delayproxy.Clock{
		Now: func() time.Time { return farEpoch },
		Wait: func(stop <-chan struct{}, d time.Duration) bool {
			f.mu.Lock()
			f.waits = append(f.waits, d)
			f.mu.Unlock()
			if f.blocked != nil {
				f.blocked <- struct{}{}
				<-stop
				return false
			}
			return true
		},
	}}
}

func (f *farClock) asked() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.waits...)
}

func farDial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetDeadline(time.Now().Add(farCeiling)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// farRoundTrip writes send in one write and reads that many bytes back.
func farRoundTrip(t *testing.T, c net.Conn, send string) string {
	t.Helper()
	if _, err := io.WriteString(c, send); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(send))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read %d bytes back: %v", len(send), err)
	}
	return string(got)
}

// Far listens on the loopback on a port the kernel chose, and what a client
// sends reaches the target and what the target answers reaches the client.
func TestFarListensOnTheLoopbackAndForwardsToItsTarget(t *testing.T) {
	t.Parallel()

	target := farEcho(t)
	fake := &farClock{}
	p := real.far(t, target, farDelay, fake.options(), net.Listen)
	host, port, err := net.SplitHostPort(p.Addr())
	if err != nil || host != "127.0.0.1" || port == "0" || port == "" {
		t.Fatalf("Far listens on %q, %v; want 127.0.0.1 and a port the kernel chose", p.Addr(), err)
	}
	if p.Addr() == target {
		t.Fatalf("Far listens on the target's own address %s", target)
	}
	c := farDial(t, p.Addr())
	if got := farRoundTrip(t, c, "across the distance"); got != "across the distance" {
		t.Fatalf("what came back is %q", got)
	}
}

// The delay Far is given is the delay each write is held, once for each write:
// three commands one after the other are three delays, a hundred in one write
// are one, and the replies are none. This is the unit test of the spec's
// three functional ones.
func TestFarAppliesTheDelayOncePerWrite(t *testing.T) {
	t.Parallel()

	fake := &farClock{}
	p := real.far(t, farEcho(t), farDelay, fake.options(), net.Listen)
	c := farDial(t, p.Addr())

	if got := farRoundTrip(t, c, farPing); got != farPing {
		t.Fatalf("one PING came back as %q", got)
	}
	if got := fake.asked(); !farOnly(got, 1) {
		t.Fatalf("one PING was held %v; want the delay once", got)
	}
	pipeline := strings.Repeat(farPing, farPipelined)
	if got := farRoundTrip(t, c, pipeline); got != pipeline {
		t.Fatalf("the pipeline of %d did not come back whole", farPipelined)
	}
	if got := fake.asked(); !farOnly(got, 2) {
		t.Fatalf("%d PINGs in one write brought the waits to %v; want the delay once more, not %d times", farPipelined, got, farPipelined)
	}
	for i := 0; i < farSeparate; i++ {
		farRoundTrip(t, c, farPing)
	}
	total := 2 + farSeparate
	if got := fake.asked(); !farOnly(got, total) {
		t.Fatalf("%d more commands, each waiting for its reply, brought the waits to %v; want %d, the delay once each", farSeparate, got, total)
	}
	if p.Writes() != total {
		t.Fatalf("the ledger counts %d writes; want %d", p.Writes(), total)
	}
}

// Far stops in the test's cleanup with nothing left running: a write that was
// held when the test ended is not sent, its client is hung up on, the
// listener is closed and no goroutine of the proxy remains. The listener is
// asked, not the port: once it is closed the port is anyone's, and a parallel
// test may take it and answer there.
func TestFarStopsInCleanupAndLeavesNothingRunning(t *testing.T) {
	t.Parallel()

	target := farEcho(t)
	var p *delayproxy.Proxy
	var ln net.Listener
	var left net.Conn
	t.Run("a test that ends with a write held", func(t *testing.T) {
		fake := &farClock{blocked: make(chan struct{}, 1)}
		p = real.far(t, target, farDelay, fake.options(), func(network, address string) (net.Listener, error) {
			var err error
			ln, err = net.Listen(network, address)
			return ln, err
		})
		left = farDial(t, p.Addr())
		if _, err := io.WriteString(left, farPing); err != nil {
			t.Fatal(err)
		}
		<-fake.blocked // held
		if got, want := p.Live(), 1+delayproxy.GoroutinesPerConn; got != want {
			t.Fatalf("Live = %d with one client; want %d: one to accept and %d for the client", got, want, delayproxy.GoroutinesPerConn)
		}
	})
	if got := p.Live(); got != 0 {
		t.Fatalf("Live = %d after the test's cleanup; want 0", got)
	}
	if got, err := io.ReadAll(left); len(got) != 0 {
		t.Fatalf("the client read %q, %v after the cleanup; want it hung up on with nothing", got, err)
	}
	if p.Writes() != 0 {
		t.Fatalf("the ledger counts %d writes of a write that was held at the end; want 0", p.Writes())
	}
	if conn, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("after the cleanup Far's listener accepts: %v; want it closed", err)
	}
	p.Stop() // a second call only waits
}

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
		"a store off the machine":     {"example.com:80", farDelay, net.Listen, "not one"},
		"a target with no port":       {"127.0.0.1", farDelay, net.Listen, "not one"},
		"a negative delay":            {"127.0.0.1:7000", -time.Millisecond, net.Listen, "delay"},
		"a delay past the longest":    {"127.0.0.1:7000", delayproxy.MaxDelay + time.Nanosecond, net.Listen, "delay"},
		"a machine with no port left": {"127.0.0.1:7000", farDelay, noPort, "no descriptors left"},
	} {
		r := provoke(t, func(tb testing.TB) { real.far(tb, c.target, c.delay, delayproxy.Options{}, c.listen) })
		if !strings.Contains(r.fatal, c.want) {
			t.Errorf("%s: failed with %q; want it to say %q", name, r.fatal, c.want)
		}
	}
	var listened atomic.Int64
	counting := func(network, address string) (net.Listener, error) {
		listened.Add(1)
		return net.Listen(network, address)
	}
	provoke(t, func(tb testing.TB) { real.far(tb, "example.com:80", farDelay, delayproxy.Options{}, counting) })
	if listened.Load() != 0 {
		t.Error("a target off the machine was refused after a listener was opened; want it refused first")
	}
}

// Like the server Start runs, Far is for a test binary and nothing else.
func TestFarRefusesToRunOutsideATestBinary(t *testing.T) {
	t.Parallel()

	l := real
	l.inTest = func() bool { return false }
	said := panics(func() { l.far(t, "127.0.0.1:7000", farDelay, delayproxy.Options{}, net.Listen) })
	if s, _ := said.(string); !strings.Contains(s, "outside a test binary") {
		t.Fatalf("Far outside a test binary panicked with %v; want the refusal", said)
	}
}
