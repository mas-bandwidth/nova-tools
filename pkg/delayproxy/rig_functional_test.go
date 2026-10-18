//go:build functional

// The proxy's rig over real loopback sockets (rig_test.go holds the plumbing that
// owns none): a socket test is a functional test.

package delayproxy

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rig is one proxy under test: an echo target, a fake clock, the proxy in
// front of it and the delay and options it serves with. The constructor holds
// the defaults -- the package delay, a standing clock, no bound -- and a test
// changes a few fields or calls a scenario method before serve.
type rig struct {
	t     *testing.T
	echo  *echo
	fake  *fakeClock
	opts  Options
	delay time.Duration
	p     *Proxy
}

// newRig starts the echo target and the standing fake clock, and holds the
// defaults; serve starts the proxy they front.
func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, echo: startEcho(t), fake: &fakeClock{}, delay: delay}
	r.opts = Options{Clock: r.fake.clock()}
	return r
}

// serve starts the proxy in front of the echo target at the delay and options
// the rig holds, on a loopback listener the test owns, and stops it when the
// test ends.
func (r *rig) serve() *Proxy {
	r.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(r.t, err, err)
	p, err := Serve(ln, r.echo.addr(), r.delay, r.opts)
	if err != nil {
		_ = ln.Close()
		require.NoError(r.t, err, "Serve: %v", err)
	}
	r.t.Cleanup(p.Stop)
	r.p = p
	return p
}

// hold is the scenario of a write looked inside while it is held: the clock
// signals on waiting and blocks its first wait until the test releases it.
func (r *rig) hold(n int) {
	r.fake.gate = make(chan struct{})
	r.fake.waiting = make(chan struct{}, n)
}

// await waits until the proxy has asked the clock for the hold the gate keeps.
func (r *rig) await() { <-r.fake.waiting }

// release ends the hold the gate keeps.
func (r *rig) release() { close(r.fake.gate) }

// client dials the proxy, with the ceiling as the read bound, and hangs it up
// when the test ends.
func (r *rig) client() net.Conn {
	r.t.Helper()
	c, err := net.Dial("tcp", r.p.Addr())
	require.NoError(r.t, err, err)
	err = c.SetDeadline(time.Now().Add(ceiling))
	require.NoError(r.t, err, err)
	r.t.Cleanup(func() { _ = c.Close() })
	return c
}

// echo is a loopback server the test owns. It sends back every byte it reads,
// says what it has received, and hangs up when its peer stops sending.
type echo struct {
	ln       net.Listener
	received atomic.Int64
	accepted atomic.Int64
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    []net.Conn
}

func startEcho(t *testing.T) *echo {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, err)
	e := &echo{ln: ln}
	t.Cleanup(func() {
		_ = ln.Close()
		e.mu.Lock()
		for _, c := range e.conns {
			_ = c.Close()
		}
		e.mu.Unlock()
		e.wg.Wait()
	})
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			e.accepted.Add(1)
			e.mu.Lock()
			e.conns = append(e.conns, c)
			e.mu.Unlock()
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				defer func() { _ = c.Close() }() // ignored: a test fixture's connection ends when the accept loop stops; the test's assertions are the report
				buf := make([]byte, echoBuffer)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						e.received.Add(int64(n))
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return e
}

func (e *echo) addr() string { return e.ln.Addr().String() }
