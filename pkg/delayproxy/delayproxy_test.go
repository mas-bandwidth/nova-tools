package delayproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests of the proxy live on the rig (rig_test.go): the echo target, the
// clocks, the listeners and the clients are its, and a test here is the scenario
// it runs and what it pins. They wait on no clock, bar the real-clock one below.

// Every write is held once, whatever it carries: three commands sent one after
// the other pay the delay three times, a hundred sent in one write pay it once,
// and the replies pay nothing.
func TestEachWriteIsHeldOnceAndAPipelinePaysOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	p := r.serve()
	c := r.client()

	separate := []string{"one", "two", "three"} // each waits for its reply
	for i, send := range separate {
		got := roundTrip(t, c, send)
		require.Equal(t, send, got, "command %d came back as %q", i, got)
	}
	asked := r.fake.asked()
	require.True(t, onlyDelays(asked, len(separate)), "%d commands, each waiting for its reply, asked the clock for %v; want the delay once each", len(separate), asked)
	got := roundTrip(t, c, pipeline)
	require.Equal(t, pipeline, got, "the pipeline did not come back whole")
	asked = r.fake.asked()
	require.True(t, onlyDelays(asked, len(separate)+1), "%d commands in one write brought the clock's waits to %v; want one more delay, not %d", pipelined, asked, pipelined)
	require.Equal(t, len(separate)+1, p.Writes(), "the proxy counts %d writes; want %d", p.Writes(), len(separate)+1)
}

// Nothing reaches the target before its delay is over. The proxy is the only
// way to the target and it is held in its wait, so what the target has received
// at that moment is what was sent early.
func TestNothingIsForwardedBeforeItsDelayIsOver(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(1)
	p := r.serve()
	c := r.client()

	_, err := io.WriteString(c, "hello")
	require.NoError(t, err, err)
	r.await() // the write is held
	n := r.echo.received.Load()
	require.Zero(t, n, "the target has received %d bytes while the write is still held", n)
	require.Zero(t, p.Writes(), "the proxy counts %d writes while the first is still held", p.Writes())
	r.release()
	got := make([]byte, len("hello"))
	_, err = io.ReadFull(c, got)
	require.NoError(t, err, "after the delay the reply is %q, %v; want hello", got, err)
	require.Equal(t, "hello", string(got), "after the delay the reply is %q, %v; want hello", got, err)
	require.Equal(t, 1, p.Writes(), "the proxy counts %d writes; want 1", p.Writes())
}

// A write larger than one read comes in as several, each of them held the delay,
// and the bytes come out whole and in order.
func TestALargeWriteComesOutWholeAndInOrder(t *testing.T) {
	t.Parallel()

	send := make([]byte, largeWrite)
	for i := range send {
		send[i] = byte(i % patternPeriod)
	}
	r := newRig(t)
	p := r.serve()
	c := r.client()

	got := roundTrip(t, c, string(send))
	require.Equal(t, string(send), got, "the large write did not come back whole and in order")
	asked := r.fake.asked()
	least := (largeWrite + ChunkBytes - 1) / ChunkBytes
	require.GreaterOrEqual(t, len(asked), least, "%d bytes came in as %d reads; want at least %d", largeWrite, len(asked), least)
	for i, d := range asked {
		require.Equal(t, delay, d, "read %d waited %v; want the delay, %v", i, d, delay)
	}
	require.Equal(t, len(asked), p.Writes(), "the proxy counts %d writes for %d reads", p.Writes(), len(asked))
}

// Reads that arrive together are each stamped at their own arrival plus the
// delay, so the second waits only what's left once the first is sent, and a run
// pays once. The proxy's halves are driven by hand over pipes, so events are
// ordered and nothing races.
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
		_, err := io.WriteString(peer, string(b))
		require.NoError(t, err, err)
	}
	_ = peer.Close()
	<-reading
	var stamped []held
	for h := range queue {
		stamped = append(stamped, h)
	}
	require.Equal(t, len(arrivals), len(stamped), "%d writes were read as %d", reads, len(stamped))
	for i, h := range stamped {
		require.True(t, h.at.Equal(arrivals[i]), "read %d is stamped at %v, due %v; want its own arrival %v and that plus the delay", i, h.at, h.due, arrivals[i])
		require.True(t, h.due.Equal(arrivals[i].Add(delay)), "read %d is stamped at %v, due %v; want its own arrival %v and that plus the delay", i, h.at, h.due, arrivals[i])
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
	_, err := io.ReadFull(sink, got)
	require.NoError(t, err, "forwarded %q, %v; want %s in order", got, err, payload)
	require.Equal(t, payload, string(got), "forwarded %q, %v; want %s in order", got, err, payload)
	<-forwarding
	_ = up.Close()
	_ = sink.Close()
	want := []time.Duration{delay}
	for len(want) < reads {
		want = append(want, apart)
	}
	require.True(t, slices.Equal(waits, want), "the %d reads waited %v; want the delay and then only what was left of each one's own: %v", reads, waits, want)
	require.Equal(t, reads, p.Writes(), "the proxy counts %d writes, shortest %v; want %d, exactly the delay", p.Writes(), p.Shortest(), reads)
	require.Equal(t, delay, p.Shortest(), "the proxy counts %d writes, shortest %v; want %d, exactly the delay", p.Writes(), p.Shortest(), reads)
}

// A client that has sent all it will and closes its sending side still gets the
// replies to what was held when it did.
func TestAClientThatStopsSendingStillGetsTheRepliesToWhatWasHeld(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(1)
	r.serve()
	c := r.client().(*net.TCPConn)

	_, err := io.WriteString(c, "abc")
	require.NoError(t, err, err)
	err = c.CloseWrite()
	require.NoError(t, err, err)
	r.await()
	r.release()
	got, err := io.ReadAll(c)
	require.NoError(t, err, "read after the client stopped sending: %q, %v; want abc", got, err)
	require.Equal(t, "abc", string(got), "read after the client stopped sending: %q, %v; want abc", got, err)
}

// Stop returns with nothing of the proxy running, though clients are mid-hold,
// and hangs up on them. What was held is never sent.
func TestStopEndsEveryGoroutineWhileWritesAreHeld(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(heldClients)
	p := r.serve()
	conns := make([]net.Conn, heldClients)
	for i := range conns {
		conns[i] = r.client()
		_, err := io.WriteString(conns[i], "held")
		require.NoError(t, err, err)
		r.await()
	}
	got, want := int(p.live.Load()), acceptLoop+GoroutinesPerConn*heldClients
	require.Equal(t, want, got, "live = %d with %d connections held; want %d: one to accept and %d each", got, heldClients, want, GoroutinesPerConn)
	p.Stop()
	got = int(p.live.Load())
	require.Zero(t, got, "live = %d after Stop returned; want 0", got)
	p.Stop() // a second call only waits
	for i, c := range conns {
		got, err := io.ReadAll(c)
		require.Zero(t, len(got), "client %d read %q, %v after Stop; want it hung up on with nothing", i, got, err)
	}
	n := r.echo.received.Load()
	require.Zero(t, n, "the target received %d bytes of writes that were held when Stop ran", n)
	require.Zero(t, p.Writes(), "the proxy counts %d writes; want 0", p.Writes())
}

// The bound is on open connections: with one allowed, a second client waits in
// the backlog, unserved and undialled, until the first is over, and is then
// served.
func TestAClientPastTheBoundWaitsForASlot(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	const slots = 1
	r.opts.MaxConns = slots
	p := r.serve()
	first := r.client()
	got := roundTrip(t, first, "first")
	require.Equal(t, "first", got, "first came back as %q", got)
	second := r.client()
	_, err := io.WriteString(second, "second")
	require.NoError(t, err, err)
	n := r.echo.accepted.Load()
	require.Equal(t, int64(slots), n, "the target has taken %d connections with %d slot and the first still open; want %d", n, slots, slots)
	live, want := int(p.live.Load()), acceptLoop+GoroutinesPerConn*slots
	require.Equal(t, want, live, "live = %d; want %d: the accept loop and the one connection", live, want)
	err = first.Close()
	require.NoError(t, err, err)
	gotBytes := make([]byte, len("second"))
	_, err = io.ReadFull(second, gotBytes)
	require.NoError(t, err, "the second client, once a slot was free, read %q, %v; want second", gotBytes, err)
	require.Equal(t, "second", string(gotBytes), "the second client, once a slot was free, read %q, %v; want second", gotBytes, err)
	n = r.echo.accepted.Load()
	require.Equal(t, int64(slots+1), n, "the target has taken %d connections; want %d", n, slots+1)
}

// A target that refuses is the client's hang-up: the proxy closes the client
// with end of stream, and logs what and why. The test gives the proxy a dial
// that refuses, so no port is opened and the client is one end of a pipe.
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
	require.NoError(t, err, err)
	t.Cleanup(p.Stop)
	c := ln.client(t)
	got, err := io.ReadAll(c) // nil when the proxy closed the client: end of stream
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		require.NotErrorIs(t, err, os.ErrDeadlineExceeded, "a client of a target that refuses was not hung up on before the ceiling (%v); want the proxy to close it", err)
	case err != nil || len(got) != 0:
		require.NoError(t, err, "a client of a target that refuses read %q, %v; want a hang-up: end of stream with nothing", got, err)
		require.Empty(t, got, "a client of a target that refuses read %q, %v; want a hang-up: end of stream with nothing", got, err)
	}
	// The proxy says so before it hangs up, so what it said is already here.
	select {
	case said := <-told:
		for _, want := range []string{"dialling", target, refused.Error()} {
			require.Contains(t, said, want, "Logf said %q; want it to name %q", said, want)
		}
	default:
		require.FailNow(t, "the client was hung up on and Logf said nothing")
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
		ln := newPipeListener()
		p, err := Serve(ln, c.target, c.delay, Options{MaxConns: c.max})
		_ = ln.Close()
		if assert.Error(t, err, "%s: Serve accepted it", name) {
			assert.Nil(t, p, "%s: Serve returned a proxy with the error", name)
			continue
		}
		p.Stop()
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
		err := Loopback(addr)
		assert.Equal(t, ok, (err == nil), "Loopback(%q) = %v; want accepted %v", addr, err, ok)
	}
}

// The real clock holds a write for at least the delay, by the proxy's own
// measure. It is the only test of this file that waits on real time.
func TestTheRealClockHoldsAWriteForAtLeastTheDelay(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.delay, r.opts = realDelay, Options{} // the real clock is the proxy's own when no clock is given
	p := r.serve()
	require.Zero(t, p.Writes(), "a new proxy counts %d writes, shortest %v; want none", p.Writes(), p.Shortest())
	require.Zero(t, p.Shortest(), "a new proxy counts %d writes, shortest %v; want none", p.Writes(), p.Shortest())
	c := r.client()
	got := roundTrip(t, c, "real clock")
	require.Equal(t, "real clock", got, "came back as %q", got)
	require.Equal(t, 1, p.Writes(), "after one write through the real clock: %d writes, shortest %v; want 1, at least %v", p.Writes(), p.Shortest(), realDelay)
	require.GreaterOrEqual(t, p.Shortest(), realDelay, "after one write through the real clock: %d writes, shortest %v; want 1, at least %v", p.Writes(), p.Shortest(), realDelay)
}

// Serve takes every delay the help documents, both ends of the range.
func TestServeAcceptsEveryDelayItDocuments(t *testing.T) {
	t.Parallel()

	for name, d := range map[string]time.Duration{"no delay": 0, "the longest delay, MaxDelay": MaxDelay} {
		p, err := Serve(newPipeListener(), "127.0.0.1:7000", d, Options{})
		if assert.NoError(t, err, "%s: Serve refused %v: %v", name, d, err) {
			p.Stop()
		}
	}
}

// One client's hold does not delay another's: each connection waits for itself,
// on no lock the proxy shares. Were the delay shared, a second client would wait
// behind the first's endless hold and fail its read at the ceiling, by name.
func TestOneClientsHoldDoesNotDelayAnother(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(2)
	r.fake.firstOnly = true // the first client's hold does not end
	r.serve()

	first := r.client()
	_, err := io.WriteString(first, "a")
	require.NoError(t, err, err)
	r.await() // the first client's write is held, and its wait does not end

	second := r.client()
	_, err = io.WriteString(second, "b")
	require.NoError(t, err, err)
	got := make([]byte, 1)
	_, err = io.ReadFull(second, got)
	require.NoError(t, err, "a client that came while another client's write was held was not answered: %v; the clients share the delay", err)
	require.Equal(t, "b", string(got), "a client that came while another client's write was held read %q; want b", got)
	n := r.echo.received.Load()
	require.Equal(t, int64(1), n, "the target has received %d bytes; want the second client's one, for the first is still held", n)
	r.release()
	_, err = io.ReadFull(first, got)
	require.NoError(t, err, "the first client, once its hold was over, read %q, %v; want a", got, err)
	require.Equal(t, "a", string(got), "the first client, once its hold was over, read %q, %v; want a", got, err)
}

// When the listener fails, the proxy closes it, so a client is refused at once and
// is not left in a backlog nothing reads. The listener here fails every Accept, as
// one does with the process out of descriptors, and tells the test when it is closed.
func TestAListenerThatFailsIsClosedSoClientsAreRefused(t *testing.T) {
	t.Parallel()

	told := make(chan string, 4)
	ln := failingListener{closed: newEvent(t)}
	p, err := Serve(ln, "127.0.0.1:7000", delay, Options{Logf: func(format string, args ...any) {
		told <- fmt.Sprintf(format, args...)
	}})
	require.NoError(t, err, err)
	t.Cleanup(p.Stop)
	require.True(t, ln.closed.wait(), "Accept failed and the proxy left its listener open: a client would wait in the backlog and never be served")
	// The proxy says so before it closes, so what it said is already here.
	select {
	case said := <-told:
		require.Contains(t, said, errAccept.Error(), "Logf said %q; want the error Accept returned, %q", said, errAccept)
	default:
		require.FailNow(t, "the listener was closed and Logf said nothing")
	}
}

// The window is three numbers the docs state in words. The pipeline test that
// follows drives the proxy through the constants symbolically, so it follows a
// change of any of them and cannot see that one happened: a queue of half the size
// is caught, but inFlight at 512 passes every unit test. This test makes a change
// to the window a visible edit: the words in delayproxy.go and far.go name each
// number and change with it, or it fails; fardelay's help reads WindowBytes.
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
		assert.Equal(t, c.want, c.got, "%s is %d, want %d: the docs say %q in %s; change the words with the number, or put the number back", c.name, c.got, c.want, c.said, c.where)
	}
}

// A pipeline pays the delay once while it fits the window and once more for each
// further window. The window is WindowBytes: inFlight reads of ChunkBytes that a
// connection holds between the client and the target, and a client that writes
// more is held back until they have gone, so what it writes then is stamped
// later. The proxy owns no socket (its ends are pipes), each write is one read,
// and the stepped clock moves only when the test lets a wait end, so what is
// counted is how many delays the clock was asked for.
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
	require.NoError(t, err, err)
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
	// the hold have all been stamped, and then ends it: the connection holds
	// inFlight reads queued, the one the forwarding half has in hand and the one
	// the reading half has stamped and cannot yet queue. A read stamped after the
	// hold ends is stamped a delay later, and is held a delay of its own.
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
				require.NoError(t, err, "the client's write was not read before the ceiling: %v; the proxy took %d reads while holding one, want %d", err, step.stamps(sent), want)
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
	require.NoError(t, got.err, "the target received %d of %d bytes: %v", got.n, total, got.err)
	require.Equal(t, total, got.n, "the target received %d of %d bytes: %v", got.n, total, got.err)
	asked := step.asked()
	require.True(t, onlyDelays(asked, windows), "%d reads, %d windows of %d, asked the clock for %v; want the delay %d times, once for each window", chunks, windows, inFlight, asked, windows)
}

// heldOutsideTheQueue is what a connection holds beyond the inFlight reads in its
// queue while the forwarding half waits: the read that half has taken out to
// wait for, and the read the reading half has stamped and cannot yet queue.
const heldOutsideTheQueue = 2
