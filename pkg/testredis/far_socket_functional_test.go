//go:build functional

package testredis

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/delayproxy"
	"github.com/stretchr/testify/require"
)

// farCeiling bounds every read of a socket here, generously: a test that is not
// answered fails at it and does not hang.
const farCeiling = 30 * time.Second

// farPing is one command of a pipeline, PING as a RESP array of 14 bytes.
const farPing = "*1\r\n$4\r\nPING\r\n"

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
		require.NoError(t, err, err)
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
				// ignored: the echo ends when the test's client hangs up; the test's own assertions are the report
				defer func() { _ = c.Close() }()
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
		require.NoError(t, err, err)
	}
	if err := c.SetDeadline(time.Now().Add(farCeiling)); err != nil {
		require.NoError(t, err, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// farDialOutliving is farDial for a client that must outlive the test that dials
// it: a failure is sub's, and the client is closed at the end of owner, the
// parent, after every cleanup of sub has run. It is what lets a test read what the
// proxy did to a client from outside the test the proxy lived in.
func farDialOutliving(sub, owner *testing.T, addr string) net.Conn {
	sub.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		require.NoError(sub, err, err)
	}
	if err := c.SetDeadline(time.Now().Add(farCeiling)); err != nil {
		require.NoError(sub, err, err)
	}
	owner.Cleanup(func() { _ = c.Close() })
	return c
}

// farRoundTrip writes send in one write and reads that many bytes back.
func farRoundTrip(t *testing.T, c net.Conn, send string) string {
	t.Helper()
	if _, err := io.WriteString(c, send); err != nil {
		require.NoError(t, err, "write: %v", err)
	}
	got := make([]byte, len(send))
	if _, err := io.ReadFull(c, got); err != nil {
		require.NoError(t, err, "read %d bytes back: %v", len(send), err)
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
		require.Failf(t, "", "Far listens on %q, %v; want 127.0.0.1 and a port the kernel chose", p.Addr(), err)
	}
	if p.Addr() == target {
		require.Failf(t, "", "Far listens on the target's own address %s", target)
	}
	c := farDial(t, p.Addr())
	if got := farRoundTrip(t, c, "across the distance"); got != "across the distance" {
		require.Equal(t, "across the distance", got, "what came back is %q", got)
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
		require.Equal(t, farPing, got, "one PING came back as %q", got)
	}
	if got := fake.asked(); !farOnly(got, 1) {
		require.Failf(t, "", "one PING was held %v; want the delay once", got)
	}
	pipeline := strings.Repeat(farPing, farPipelined)
	if got := farRoundTrip(t, c, pipeline); got != pipeline {
		require.Equal(t, pipeline, got, "the pipeline of %d did not come back whole", farPipelined)
	}
	if got := fake.asked(); !farOnly(got, 2) {
		require.Failf(t, "", "%d PINGs in one write brought the waits to %v; want the delay once more, not %d times", farPipelined, got, farPipelined)
	}
	for i := 0; i < farSeparate; i++ {
		farRoundTrip(t, c, farPing)
	}
	total := 2 + farSeparate
	if got := fake.asked(); !farOnly(got, total) {
		require.Failf(t, "", "%d more commands, each waiting for its reply, brought the waits to %v; want %d, the delay once each", farSeparate, got, total)
	}
	if p.Writes() != total {
		require.Failf(t, "", "the ledger counts %d writes; want %d", p.Writes(), total)
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
	var live atomic.Int64
	t.Run("a test that ends with a write held", func(sub *testing.T) {
		fake := &farClock{blocked: make(chan struct{}, 1)}
		opts := fake.options()
		opts.Live = &live
		p = real.far(sub, target, farDelay, opts, func(network, address string) (net.Listener, error) {
			var err error
			ln, err = net.Listen(network, address)
			return ln, err
		})
		// The client belongs to the parent: dialled on sub it would be closed by
		// sub's cleanup, before the proxy's, and what it reads afterwards would be
		// its own closed socket and never the proxy's hang-up.
		left = farDialOutliving(sub, t, p.Addr())
		if _, err := io.WriteString(left, farPing); err != nil {
			require.NoError(sub, err, err)
		}
		<-fake.blocked // held
		if got, want := int(live.Load()), 1+delayproxy.GoroutinesPerConn; got != want {
			require.Equal(sub, want, got, "Live = %d with one client; want %d: one to accept and %d for the client", got, want, delayproxy.GoroutinesPerConn)
		}
	})
	if got := int(live.Load()); got != 0 {
		require.Zero(t, got, "Live = %d after the test's cleanup; want 0", got)
	}
	got, err := io.ReadAll(left)
	switch {
	case errors.Is(err, net.ErrClosed):
		require.Failf(t, "", "the client read its own closed socket (%v); it must outlive the test the proxy ran in, or it cannot tell whether the proxy hung up on it", err)
	case errors.Is(err, os.ErrDeadlineExceeded):
		require.Failf(t, "", "the client was not hung up on before the ceiling (%v); want the proxy to close it when the test ends", err)
	case len(got) != 0:
		require.Failf(t, "", "the client read %q, %v after the cleanup; want it hung up on with nothing", got, err)
	}
	if p.Writes() != 0 {
		require.Failf(t, "", "the ledger counts %d writes of a write that was held at the end; want 0", p.Writes())
	}
	if conn, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		require.Failf(t, "", "after the cleanup Far's listener accepts: %v; want it closed", err)
	}
	p.Stop() // a second call only waits
}
