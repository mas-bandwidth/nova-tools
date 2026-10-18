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
