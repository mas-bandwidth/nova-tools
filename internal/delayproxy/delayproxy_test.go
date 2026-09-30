package delayproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests wait on no clock, bar one marked below. The proxy is given a
// Clock whose waits are recorded and, where a test wants to look inside a wait, held on a gate: what
// is proved is how many delays were paid and that nothing was sent before its
// delay was over, never how long a machine took. The sockets are loopback ones
// this file owns: the proxy's listener, an echo server standing for the target,
// and the clients.

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
	if err != nil {
		t.Fatal(err)
	}
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
				defer c.Close()
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

// serveTo starts a proxy in front of target on a loopback listener the test
// owns, and stops it at the end of the test.
func serveTo(t *testing.T, target string, opts Options) *Proxy {
	t.Helper()
	return serveAfter(t, target, delay, opts)
}

// serveAfter is serveTo with a delay of its own.
func serveAfter(t *testing.T, target string, after time.Duration, opts Options) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Serve(ln, target, after, opts)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(p.Stop)
	return p
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetDeadline(time.Now().Add(ceiling)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// roundTrip writes send in one write and reads that many bytes back.
func roundTrip(t *testing.T, c net.Conn, send string) string {
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

// Every write is held once, whatever it carries: three commands sent one after
// the other pay the delay three times, a hundred sent in one write pay it once,
// and the replies pay nothing.
func TestEachWriteIsHeldOnceAndAPipelinePaysOnce(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	fake := &fakeClock{}
	p := serveTo(t, target.addr(), Options{Clock: fake.clock()})
	c := dial(t, p.Addr())

	separate := []string{"one", "two", "three"} // each waits for its reply
	for i, send := range separate {
		if got := roundTrip(t, c, send); got != send {
			t.Fatalf("command %d came back as %q", i, got)
		}
	}
	if got := fake.asked(); !onlyDelays(got, len(separate)) {
		t.Fatalf("%d commands, each waiting for its reply, asked the clock for %v; want the delay once each", len(separate), got)
	}
	if got := roundTrip(t, c, pipeline); got != pipeline {
		t.Fatal("the pipeline did not come back whole")
	}
	if got := fake.asked(); !onlyDelays(got, len(separate)+1) {
		t.Fatalf("%d commands in one write brought the clock's waits to %v; want one more delay, not %d", pipelined, got, pipelined)
	}
	if p.Writes() != len(separate)+1 {
		t.Fatalf("the proxy counts %d writes; want %d", p.Writes(), len(separate)+1)
	}
}

// Nothing reaches the target before its delay is over. The proxy is the only
// way to the target and it is held in its wait, so what the target has received
// at that moment is what was sent early.
func TestNothingIsForwardedBeforeItsDelayIsOver(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	fake := &fakeClock{gate: make(chan struct{}), waiting: make(chan struct{}, 1)}
	p := serveTo(t, target.addr(), Options{Clock: fake.clock()})
	c := dial(t, p.Addr())

	if _, err := io.WriteString(c, "hello"); err != nil {
		t.Fatal(err)
	}
	<-fake.waiting // the write is held
	if n := target.received.Load(); n != 0 {
		t.Fatalf("the target has received %d bytes while the write is still held", n)
	}
	if p.Writes() != 0 {
		t.Fatalf("the proxy counts %d writes while the first is still held", p.Writes())
	}
	close(fake.gate)
	got := make([]byte, len("hello"))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello" {
		t.Fatalf("after the delay the reply is %q, %v; want hello", got, err)
	}
	if p.Writes() != 1 {
		t.Fatalf("the proxy counts %d writes; want 1", p.Writes())
	}
}

// A write larger than one read comes in as several, each of them held the delay,
// and the bytes come out whole and in order.
func TestALargeWriteComesOutWholeAndInOrder(t *testing.T) {
	t.Parallel()

	send := make([]byte, largeWrite)
	for i := range send {
		send[i] = byte(i % patternPeriod)
	}
	target := startEcho(t)
	fake := &fakeClock{}
	p := serveTo(t, target.addr(), Options{Clock: fake.clock()})
	c := dial(t, p.Addr())

	if got := roundTrip(t, c, string(send)); !bytes.Equal([]byte(got), send) {
		t.Fatal("the large write did not come back whole and in order")
	}
	asked := fake.asked()
	if least := (largeWrite + ChunkBytes - 1) / ChunkBytes; len(asked) < least {
		t.Fatalf("%d bytes came in as %d reads; want at least %d", largeWrite, len(asked), least)
	}
	for i, d := range asked {
		if d != delay {
			t.Fatalf("read %d waited %v; want the delay, %v", i, d, delay)
		}
	}
	if p.Writes() != len(asked) {
		t.Fatalf("the proxy counts %d writes for %d reads", p.Writes(), len(asked))
	}
}

// The delay is a line and not a queue: reads that arrive one after another are
// each sent on at their own arrival plus the delay, so the second waits only
// what is left of its delay once the first is sent, and a run of reads pays the
// delay once. The proxy's two halves are driven by hand over pipes with a clock
// the test moves, so the order of events is the test's and nothing races.
func TestReadsThatArriveTogetherPayTheDelayOnce(t *testing.T) {
	t.Parallel()

	p := &Proxy{delay: delay}
	p.shortest.Store(noHold)
	never := make(chan struct{})
	none := func() {}

	// Reading: the clock hands out the three arrival times, one per read.
	arrivals := make([]time.Time, reads)
	for i := range arrivals {
		arrivals[i] = epoch.Add(time.Duration(i) * apart)
	}
	var next atomic.Int64
	p.clock = Clock{Now: func() time.Time { return arrivals[next.Add(1)-1] }}
	client, peer := net.Pipe()
	queue := make(chan held, len(arrivals))
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		p.read(client, queue, never, none)
	}()
	const payload = "abc" // one byte for each of the reads
	for _, b := range payload {
		if _, err := io.WriteString(peer, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	_ = peer.Close()
	<-reading
	var stamped []held
	for h := range queue {
		stamped = append(stamped, h)
	}
	if len(stamped) != len(arrivals) {
		t.Fatalf("%d writes were read as %d", reads, len(stamped))
	}
	for i, h := range stamped {
		if !h.at.Equal(arrivals[i]) || !h.due.Equal(arrivals[i].Add(delay)) {
			t.Fatalf("read %d is stamped at %v, due %v; want its own arrival %v and that plus the delay", i, h.at, h.due, arrivals[i])
		}
	}

	// Forwarding: the clock stands at the first arrival, and a wait moves it on
	// by what was waited.
	var mu sync.Mutex
	now := epoch
	var waits []time.Duration
	p.clock = Clock{
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		Wait: func(_ <-chan struct{}, d time.Duration) bool {
			mu.Lock()
			defer mu.Unlock()
			waits = append(waits, d)
			now = now.Add(d)
			return true
		},
	}
	up, sink := net.Pipe()
	queue = make(chan held, len(stamped))
	for _, h := range stamped {
		queue <- h
	}
	close(queue)
	forwarding := make(chan struct{})
	go func() {
		defer close(forwarding)
		p.forward(up, queue, never, none)
	}()
	got := make([]byte, len(stamped))
	if _, err := io.ReadFull(sink, got); err != nil || string(got) != payload {
		t.Fatalf("forwarded %q, %v; want %s in order", got, err, payload)
	}
	<-forwarding
	_ = up.Close()
	_ = sink.Close()
	want := []time.Duration{delay}
	for len(want) < reads {
		want = append(want, apart)
	}
	if !slices.Equal(waits, want) {
		t.Fatalf("the %d reads waited %v; want the delay and then only what was left of each one's own: %v", reads, waits, want)
	}
	if p.Writes() != reads || p.Shortest() != delay {
		t.Fatalf("the proxy counts %d writes, shortest %v; want %d, exactly the delay", p.Writes(), p.Shortest(), reads)
	}
}

// A client that has sent all it will and closes its sending side still gets the
// replies to what was held when it did.
func TestAClientThatStopsSendingStillGetsTheRepliesToWhatWasHeld(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	fake := &fakeClock{gate: make(chan struct{}), waiting: make(chan struct{}, 1)}
	p := serveTo(t, target.addr(), Options{Clock: fake.clock()})
	c := dial(t, p.Addr()).(*net.TCPConn)

	if _, err := io.WriteString(c, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	<-fake.waiting
	close(fake.gate)
	if got, err := io.ReadAll(c); err != nil || string(got) != "abc" {
		t.Fatalf("read after the client stopped sending: %q, %v; want abc", got, err)
	}
}

// Stop returns with nothing of the proxy running, though clients are mid-hold,
// and hangs up on them. What was held is never sent.
func TestStopEndsEveryGoroutineWhileWritesAreHeld(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	fake := &fakeClock{gate: make(chan struct{}), waiting: make(chan struct{}, heldClients)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Serve(ln, target.addr(), delay, Options{Clock: fake.clock()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	conns := make([]net.Conn, heldClients)
	for i := range conns {
		conns[i] = dial(t, p.Addr())
		if _, err := io.WriteString(conns[i], "held"); err != nil {
			t.Fatal(err)
		}
		<-fake.waiting
	}
	if got, want := p.Live(), acceptLoop+GoroutinesPerConn*heldClients; got != want {
		t.Fatalf("Live = %d with %d connections held; want %d: one to accept and %d each", got, heldClients, want, GoroutinesPerConn)
	}
	p.Stop()
	if got := p.Live(); got != 0 {
		t.Fatalf("Live = %d after Stop returned; want 0", got)
	}
	p.Stop() // a second call only waits
	for i, c := range conns {
		if got, err := io.ReadAll(c); len(got) != 0 {
			t.Fatalf("client %d read %q, %v after Stop; want it hung up on with nothing", i, got, err)
		}
	}
	if n := target.received.Load(); n != 0 {
		t.Fatalf("the target received %d bytes of writes that were held when Stop ran", n)
	}
	if p.Writes() != 0 {
		t.Fatalf("the proxy counts %d writes; want 0", p.Writes())
	}
}

// The bound is on open connections: with one allowed, a second client waits in
// the backlog, unserved and undialled, until the first is over, and is then
// served.
func TestAClientPastTheBoundWaitsForASlot(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	fake := &fakeClock{}
	const slots = 1
	p := serveTo(t, target.addr(), Options{Clock: fake.clock(), MaxConns: slots})
	first := dial(t, p.Addr())
	if got := roundTrip(t, first, "first"); got != "first" {
		t.Fatalf("first came back as %q", got)
	}
	second := dial(t, p.Addr())
	if _, err := io.WriteString(second, "second"); err != nil {
		t.Fatal(err)
	}
	if n := target.accepted.Load(); n != slots {
		t.Fatalf("the target has taken %d connections with %d slot and the first still open; want %d", n, slots, slots)
	}
	if got, want := p.Live(), acceptLoop+GoroutinesPerConn*slots; got != want {
		t.Fatalf("Live = %d; want %d: the accept loop and the one connection", got, want)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("second"))
	if _, err := io.ReadFull(second, got); err != nil || string(got) != "second" {
		t.Fatalf("the second client, once a slot was free, read %q, %v; want second", got, err)
	}
	if n := target.accepted.Load(); n != slots+1 {
		t.Fatalf("the target has taken %d connections; want %d", n, slots+1)
	}
}

// A target that does not answer is the client's hang-up, and the proxy says
// which target, and why, through Logf. The proxy is given a dial that refuses,
// so the test dials no port and owns no socket: the client is one end of a pipe.
// The hang-up is observed: the client's read ends in end of stream, and a proxy
// that left the client open fails it by name when the read gives up at the
// ceiling, where a read that only found nothing would pass a proxy that said
// nothing to the client at all.
func TestATargetThatRefusesHangsUpTheClient(t *testing.T) {
	t.Parallel()

	const target = "127.0.0.1:7000"
	refused := errors.New("connection refused")
	told := make(chan string, 4)
	ln := newPipeListener()
	p, err := Serve(ln, target, delay, Options{
		Dial: func(context.Context, string, string) (net.Conn, error) { return nil, refused },
		Logf: func(format string, args ...any) { told <- fmt.Sprintf(format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	c := ln.client(t)
	got, err := io.ReadAll(c) // nil when the proxy closed the client: end of stream
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		t.Fatalf("a client of a target that refuses was not hung up on before the ceiling (%v); want the proxy to close it", err)
	case err != nil || len(got) != 0:
		t.Fatalf("a client of a target that refuses read %q, %v; want a hang-up: end of stream with nothing", got, err)
	}
	// The proxy says so before it hangs up, so what it said is already here.
	select {
	case said := <-told:
		for _, want := range []string{"dialling", target, refused.Error()} {
			if !strings.Contains(said, want) {
				t.Fatalf("Logf said %q; want it to name %q", said, want)
			}
		}
	default:
		t.Fatal("the client was hung up on and Logf said nothing")
	}
}

// Serve refuses what it cannot serve and starts nothing.
func TestServeRefusesWhatItCannotServe(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		target string
		delay  time.Duration
		max    int
	}{
		"a target with no port":       {"127.0.0.1", delay, 0},
		"a target with port zero":     {"127.0.0.1:0", delay, 0},
		"a target with a huge port":   {"127.0.0.1:65536", delay, 0},
		"a target with no host":       {":7000", delay, 0},
		"a target that is no address": {"not an address", delay, 0},
		"a negative delay":            {"127.0.0.1:7000", -time.Nanosecond, 0},
		"a delay past MaxDelay":       {"127.0.0.1:7000", MaxDelay + time.Nanosecond, 0},
		"a negative bound":            {"127.0.0.1:7000", delay, -1},
	} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if p, err := Serve(ln, c.target, c.delay, Options{MaxConns: c.max}); err == nil {
			p.Stop()
			t.Errorf("%s: Serve accepted it", name)
		}
		_ = ln.Close()
	}
}

// Loopback is the rule of a caller that must not be turned into a way out of
// the machine.
func TestLoopbackAcceptsOnlyTheMachinesOwn(t *testing.T) {
	t.Parallel()

	for addr, ok := range map[string]bool{
		"127.0.0.1:7000":   true,
		"127.0.0.1:0":      true,
		"[::1]:7000":       true,
		"localhost:7000":   true,
		"example.com:80":   false,
		"store.invalid:80": false,
		"127.0.0.1":        false,
		":7000":            false,
		"127.0.0.1:65536":  false,
		"127.0.0.1:port":   false,
	} {
		if err := Loopback(addr); (err == nil) != ok {
			t.Errorf("Loopback(%q) = %v; want accepted %v", addr, err, ok)
		}
	}
}

// The real clock is the one the proxy runs with everywhere but here, and its
// wait is a timer: a write is held for at least the delay by the proxy's own
// measure. The delay is the least a timer can be asked for, so the test's cost
// is not the point, and it is the only test of this file that waits on time.
func TestTheRealClockHoldsAWriteForAtLeastTheDelay(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	p := serveAfter(t, target.addr(), realDelay, Options{})
	if p.Writes() != 0 || p.Shortest() != 0 {
		t.Fatalf("a new proxy counts %d writes, shortest %v; want none", p.Writes(), p.Shortest())
	}
	c := dial(t, p.Addr())
	if got := roundTrip(t, c, "real clock"); got != "real clock" {
		t.Fatalf("came back as %q", got)
	}
	if p.Writes() != 1 || p.Shortest() < realDelay {
		t.Fatalf("after one write through the real clock: %d writes, shortest %v; want 1, at least %v", p.Writes(), p.Shortest(), realDelay)
	}
}

// Serve takes every delay the help documents, both ends of the range.
func TestServeAcceptsEveryDelayItDocuments(t *testing.T) {
	t.Parallel()

	for name, d := range map[string]time.Duration{"no delay": 0, "the longest delay, MaxDelay": MaxDelay} {
		p, err := Serve(newPipeListener(), "127.0.0.1:7000", d, Options{})
		if err != nil {
			t.Errorf("%s: Serve refused %v: %v", name, d, err)
			continue
		}
		p.Stop()
	}
}

// One client's hold does not delay another's: each connection waits for itself,
// on no lock the proxy shares. The first write is held, and stays held; a second
// client that comes after it is served meanwhile. Were the delay shared, the
// second would wait behind the first and fail its read at the ceiling, by name.
func TestOneClientsHoldDoesNotDelayAnother(t *testing.T) {
	t.Parallel()

	target := startEcho(t)
	fake := &fakeClock{gate: make(chan struct{}), waiting: make(chan struct{}, 2), firstOnly: true}
	p := serveTo(t, target.addr(), Options{Clock: fake.clock()})

	first := dial(t, p.Addr())
	if _, err := io.WriteString(first, "a"); err != nil {
		t.Fatal(err)
	}
	<-fake.waiting // the first client's write is held, and its wait does not end

	second := dial(t, p.Addr())
	if _, err := io.WriteString(second, "b"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 1)
	if _, err := io.ReadFull(second, got); err != nil {
		t.Fatalf("a client that came while another client's write was held was not answered: %v; the clients share the delay", err)
	} else if string(got) != "b" {
		t.Fatalf("a client that came while another client's write was held read %q; want b", got)
	}
	if n := target.received.Load(); n != 1 {
		t.Fatalf("the target has received %d bytes; want the second client's one, for the first is still held", n)
	}
	close(fake.gate)
	if _, err := io.ReadFull(first, got); err != nil || string(got) != "a" {
		t.Fatalf("the first client, once its hold was over, read %q, %v; want a", got, err)
	}
}

// When the listener fails, the proxy closes it: a client is then refused at once,
// and is not left in a backlog nothing reads until its own timeout. The listener
// here fails every Accept, as one does with the process out of descriptors, and
// tells the test when it is closed.
func TestAListenerThatFailsIsClosedSoClientsAreRefused(t *testing.T) {
	t.Parallel()

	told := make(chan string, 4)
	ln := failingListener{closed: newEvent(t)}
	p, err := Serve(ln, "127.0.0.1:7000", delay, Options{Logf: func(format string, args ...any) {
		told <- fmt.Sprintf(format, args...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	if !ln.closed.wait() {
		t.Fatal("Accept failed and the proxy left its listener open: a client would wait in the backlog and never be served")
	}
	// The proxy says so before it closes, so what it said is already here.
	select {
	case said := <-told:
		if !strings.Contains(said, errAccept.Error()) {
			t.Fatalf("Logf said %q; want the error Accept returned, %q", said, errAccept)
		}
	default:
		t.Fatal("the listener was closed and Logf said nothing")
	}
}

// The window is three numbers the docs state in words. The window test that
// follows drives the proxy through the constants symbolically, so it follows a
// change of any of them and cannot see that one happened: a queue of half the
// size is caught, but inFlight at 128 or at 512 passes every unit test. This test
// makes a change to the window a visible edit: the words in delayproxy.go (the
// package doc and Writes) and in far.go say 16 KiB, 256 reads and 4 MiB, and
// change with the number, or it fails. The 4 MiB is also the figure fardelay's
// help prints, read from WindowBytes, so the help follows on its own.
func TestTheWindowIsTheSizeTheDocsSay(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name      string
		got, want int
		said      string // what the docs say
		where     string // the files that say it
	}{
		{"ChunkBytes", ChunkBytes, 16 << 10, "16 KiB", "delayproxy.go and far.go"},
		{"inFlight", inFlight, 256, "256 reads", "delayproxy.go"},
		{"WindowBytes", WindowBytes, 4 << 20, "4 MiB", "delayproxy.go and far.go"},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d, want %d: the docs say %q in %s; change the words with the number, or put the number back", c.name, c.got, c.want, c.said, c.where)
		}
	}
}

// A pipeline pays the delay once while it fits the window and once more for each
// further window. The window is WindowBytes: inFlight reads of ChunkBytes that a
// connection holds between the client and the target, and a client that writes
// more is held back until they have gone, so what it writes then is stamped
// later. The client writes whole windows of reads into a proxy that owns no
// socket: its client and its target are pipes, each write is exactly one read,
// and the clock is a stepped one that moves only when the test lets a wait end,
// so what is counted is how many delays the clock was asked for.
func TestAPipelinePaysOncePerWindow(t *testing.T) {
	t.Parallel()

	for _, windows := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("%d windows", windows), func(t *testing.T) {
			t.Parallel()
			payForWindows(t, windows)
		})
	}
}

func payForWindows(t *testing.T, windows int) {
	t.Helper()

	chunks := windows * inFlight // the client writes this many reads, one write each
	total := int64(chunks) * ChunkBytes
	step := newSteppedClock()
	ln := newPipeListener()
	target := make(chan sunk, 1)
	p, err := Serve(ln, "127.0.0.1:7000", delay, Options{
		Clock: step.clock(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			proxySide, targetSide := net.Pipe()
			go takeAll(targetSide, total, target)
			return proxySide, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	client := ln.client(t)

	written := make(chan error, 1) // how the client's writes ended
	go func() {
		buf := make([]byte, ChunkBytes)
		for i := 0; i < chunks; i++ {
			if _, err := client.Write(buf); err != nil {
				written <- err
				return
			}
		}
		written <- client.Close()
	}()

	// Each time the proxy holds a read, the test waits until the reads that share
	// the hold have all been stamped, and then ends it. They are the reads the
	// proxy can take while the one it holds is not sent: what the connection
	// holds is inFlight reads queued, the one the forwarding half has in hand and
	// the one the reading half has stamped and cannot yet queue, on top of those
	// already sent. A read stamped after the hold ends is stamped a delay later,
	// and is held a delay of its own.
	var got sunk
	ended := written // nil once the client's writes are known to have ended well
drive:
	for {
		select {
		case <-step.waiting:
		case got = <-target:
			break drive
		}
		sent := p.Writes()
		for want := min(chunks, sent+inFlight+heldOutsideTheQueue); step.stamps(sent) < want; {
			select {
			case <-step.ticked:
			case err := <-ended:
				if err != nil {
					t.Fatalf("the client's write was not read before the ceiling: %v; the proxy took %d reads while holding one, want %d", err, step.stamps(sent), want)
				}
				ended = nil
			case got = <-target:
				break drive
			}
		}
		select {
		case step.release <- struct{}{}:
		case got = <-target:
			break drive
		}
	}
	if got == (sunk{}) {
		got = <-target // the target's own read gives up at the ceiling
	}
	if got.err != nil || got.n != total {
		t.Fatalf("the target received %d of %d bytes: %v", got.n, total, got.err)
	}
	if asked := step.asked(); !onlyDelays(asked, windows) {
		t.Fatalf("%d reads, %d windows of %d, asked the clock for %v; want the delay %d times, once for each window", chunks, windows, inFlight, asked, windows)
	}
}

// heldOutsideTheQueue is what a connection holds beyond the inFlight reads in its
// queue while the forwarding half waits: the read that half has taken out to
// wait for, and the read the reading half has stamped and cannot yet queue.
const heldOutsideTheQueue = 2

// sunk is what a target that only takes bytes received, and how it ended.
type sunk struct {
	n   int64
	err error
}

// takeAll is a target that reads total bytes from c and hangs up, and says what it
// got. It gives up at the ceiling.
func takeAll(c net.Conn, total int64, done chan<- sunk) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(ceiling))
	n, err := io.CopyN(io.Discard, c, total)
	done <- sunk{n: n, err: err}
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
	if err := client.SetDeadline(time.Now().Add(ceiling)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	select {
	case l.conns <- server:
	case <-l.done:
		t.Fatal("the listener is closed")
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
	if err := watch.SetReadDeadline(time.Now().Add(ceiling)); err != nil {
		t.Fatal(err)
	}
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
