// The rig of the proxy's tests: the plumbing every test of it shares, in one
// file, so a test says the scenario it runs and not how the sockets and clocks
// are wired (docs/STANDARD.md, Tests: shared rigs live beside the package's
// tests, one constructor with defaults and scenarios over copies).
//
// These tests wait on no clock, bar the one real-clock scenario marked below.
// The proxy is given a Clock whose waits are recorded and, where a test wants
// to look inside a wait, held on a gate: what is proved is how many delays
// were paid and that nothing was sent before its delay was over, never how
// long a machine took. The sockets are loopback ones the rig owns: the proxy's
// listener, an echo server standing for the target, and the clients.

package delayproxy

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The delay the tests ask for. With the fake clock it is a number the proxy is
// told, never a time that passes.
const delay = 100 * time.Millisecond

// ceiling bounds every read of a socket in these tests, generously: a test that
// is not answered fails at it and does not hang.
const ceiling = 30 * time.Second

// ping is one command of a pipeline: PING as a RESP array, 14 bytes.
const ping = "*1\r\n$4\r\nPING\r\n"

// pipelined is how many commands the pipeline holds, and pipeline is that many
// in one string: one write, well inside one read of the proxy's.
const pipelined = 100

var pipeline = strings.Repeat(ping, pipelined)

// acceptLoop is the goroutine a proxy runs to accept, beside the
// GoroutinesPerConn of each open connection.
const acceptLoop = 1

// echoBuffer is what the echo server reads at a time.
const echoBuffer = 4096

// largeWrite is a write of several reads, and patternPeriod is the period of
// the bytes it carries: a prime, so no read of ChunkBytes ends where the
// pattern starts over and a read that lost or repeated a byte cannot pass.
const (
	largeWrite    = 100 << 10
	patternPeriod = 251
)

// apart is the time between one read of a client and the next in the test that
// drives the two halves of the proxy by hand, and reads is how many there are.
const (
	apart = time.Millisecond
	reads = 3
)

// heldClients is how many clients the Stop test leaves mid-hold.
const heldClients = 4

// realDelay is the least a timer can be asked for and the delay of the one test
// that waits on time.
const realDelay = 2 * time.Millisecond

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

// onlyDelays reports whether the clock was asked for exactly n waits, each of
// the whole delay.
func onlyDelays(asked []time.Duration, n int) bool {
	if len(asked) != n {
		return false
	}
	for _, d := range asked {
		if d != delay {
			return false
		}
	}
	return true
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

// fakeClock stands still: Now never moves, so a write's delay is always the
// whole delay. It writes down every wait it is asked for. Given a gate, a wait
// signals on waiting and then blocks until the gate is closed or the connection
// ends; with firstOnly, only the first wait it is asked for blocks, and every
// later one returns at once.
type fakeClock struct {
	mu        sync.Mutex
	waits     []time.Duration
	gate      chan struct{}
	waiting   chan struct{}
	firstOnly bool
}

var epoch = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func (f *fakeClock) clock() Clock {
	return Clock{
		Now: func() time.Time { return epoch },
		Wait: func(stop <-chan struct{}, d time.Duration) bool {
			f.mu.Lock()
			f.waits = append(f.waits, d)
			first := len(f.waits) == 1
			f.mu.Unlock()
			if f.waiting != nil {
				f.waiting <- struct{}{}
			}
			if f.gate != nil && (first || !f.firstOnly) {
				select {
				case <-f.gate:
				case <-stop:
					return false
				}
			}
			return true
		},
	}
}

func (f *fakeClock) asked() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.waits...)
}

// roundTrip writes send in one write and reads that many bytes back.
func roundTrip(t *testing.T, c net.Conn, send string) string {
	t.Helper()
	_, err := io.WriteString(c, send)
	require.NoError(t, err, "write: %v", err)
	got := make([]byte, len(send))
	_, err = io.ReadFull(c, got)
	require.NoError(t, err, "read %d bytes back: %v", len(send), err)
	return string(got)
}

// steppedClock is a clock that moves only when the test lets it: Now is the time
// so far, and a Wait writes itself down, says so on waiting, blocks until the
// test releases it or the connection ends, and then moves the time on by what it
// waited. It counts every call of Now and ticks on each, so a test can wait for
// the reads the proxy stamps.
type steppedClock struct {
	mu      sync.Mutex
	now     time.Time
	calls   int
	waits   []time.Duration
	waiting chan struct{}
	release chan struct{}
	ticked  chan struct{} // one tick is pending while a call of Now has not been seen
}

func newSteppedClock() *steppedClock {
	return &steppedClock{
		now:     epoch,
		waiting: make(chan struct{}, 8),
		release: make(chan struct{}),
		ticked:  make(chan struct{}, 1),
	}
}

// stamps is how many reads the reading half has stamped, given that the forwarding
// half is held in a wait and has sent writes writes. That half asks the time twice
// for each write it has sent, before it waits and when it sends, and once for the
// one it holds; every other call is a stamp.
func (c *steppedClock) stamps(writes int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls - 2*writes - 1
}

func (c *steppedClock) clock() Clock {
	return Clock{
		Now: func() time.Time {
			c.mu.Lock()
			c.calls++
			now := c.now
			c.mu.Unlock()
			select {
			case c.ticked <- struct{}{}:
			default:
			}
			return now
		},
		Wait: func(stop <-chan struct{}, d time.Duration) bool {
			c.mu.Lock()
			c.waits = append(c.waits, d)
			c.mu.Unlock()
			select {
			case c.waiting <- struct{}{}:
			case <-stop:
				return false
			}
			select {
			case <-c.release:
				c.mu.Lock()
				c.now = c.now.Add(d)
				c.mu.Unlock()
				return true
			case <-stop:
				return false
			}
		},
	}
}

func (c *steppedClock) asked() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

// sunk is what a target that only takes bytes received, and how it ended.
type sunk struct {
	n   int64
	err error
}

// takeAll is a target that reads total bytes from c and hangs up, and says what it
// got. It gives up at the ceiling.
func takeAll(c net.Conn, total int64, done chan<- sunk) {
	defer func() { _ = c.Close() }() // ignored: a test fixture's connection ends when the copy does; the test's assertions are the report
	_ = c.SetReadDeadline(time.Now().Add(ceiling))
	n, err := io.CopyN(io.Discard, c, total)
	done <- sunk{n: n, err: err}
}

// pipeListener is a listener that owns no port: a test hands it one end of a
// pipe and the proxy accepts the other.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// client is a new client of the proxy: one end of a pipe, the other handed to
// the proxy's Accept. Its reads and writes give up at the ceiling.
func (l *pipeListener) client(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	// The deadline is set before the proxy has the other end: a pipe refuses a
	// deadline once either end is closed, and a proxy that hangs up at once closes it.
	err := client.SetDeadline(time.Now().Add(ceiling))
	require.NoError(t, err, err)
	t.Cleanup(func() { _ = client.Close() })
	select {
	case l.conns <- server:
	case <-l.done:
		require.FailNow(t, "the listener is closed")
	}
	return client
}

// pipeAddr is the address of a listener that owns no port.
type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// errAccept is what a failing listener's Accept returns.
var errAccept = errors.New("too many open files")

// failingListener is a listener whose Accept fails at once, and that fires its
// closed event when it is closed.
type failingListener struct{ closed *event }

func (failingListener) Accept() (net.Conn, error) { return nil, errAccept }
func (l failingListener) Close() error            { l.closed.fire(); return nil }
func (failingListener) Addr() net.Addr            { return pipeAddr{} }

// event is a signal a test waits on with a bound that fails by name, where a
// bare receive would hang. It is a pipe: firing is closing one end, waiting is a
// read of the other that gives up at the ceiling, like every socket here.
type event struct{ fired, watch net.Conn }

// newEvent starts the ceiling: a pipe refuses a deadline once an end is closed, so
// it is set here, before the event can fire.
func newEvent(t *testing.T) *event {
	t.Helper()
	fired, watch := net.Pipe()
	// newEvent starts the ceiling: a pipe refuses a deadline once an end is closed, so
	// it is set here, before the event can fire.
	err := watch.SetReadDeadline(time.Now().Add(ceiling))
	require.NoError(t, err, err)
	t.Cleanup(func() {
		_ = fired.Close()
		_ = watch.Close()
	})
	return &event{fired: fired, watch: watch}
}

func (e *event) fire() { _ = e.fired.Close() }

// wait reports whether the event fired before the ceiling, which runs from
// newEvent.
func (e *event) wait() bool {
	_, err := e.watch.Read(make([]byte, 1))
	return errors.Is(err, io.EOF)
}
