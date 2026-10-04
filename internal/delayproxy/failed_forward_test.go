package delayproxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// errWrite is what a target that cannot be written to returns in these tests.
var errWrite = errors.New("write failed")

// fakeTarget is a target that owns no socket: the upstream of forward in these
// tests. It writes down every chunk it is offered and answers each write from a
// script, so a failing, short or full write is the test's choice and no port is
// opened.
type fakeTarget struct {
	mu     sync.Mutex
	chunks []string
	steps  []step
	next   int
	closed int
}

// step is one answer of fakeTarget.Write: an error, a short write, or a full
// write.
type step struct {
	err   error
	short bool
}

func (f *fakeTarget) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks = append(f.chunks, string(p))
	var s step
	if f.next < len(f.steps) {
		s = f.steps[f.next]
	}
	f.next++
	if s.err != nil {
		return 0, s.err
	}
	if s.short && len(p) > 0 {
		return len(p) - 1, nil
	}
	return len(p), nil
}

// offered is the chunks the target has been asked to write, in order.
func (f *fakeTarget) offered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.chunks...)
}

func (f *fakeTarget) CloseWrite() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeTarget) Read([]byte) (int, error)         { return 0, io.EOF }
func (f *fakeTarget) Close() error                     { return nil }
func (f *fakeTarget) LocalAddr() net.Addr              { return pipeAddr{} }
func (f *fakeTarget) RemoteAddr() net.Addr             { return pipeAddr{} }
func (f *fakeTarget) SetDeadline(time.Time) error      { return nil }
func (f *fakeTarget) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeTarget) SetWriteDeadline(time.Time) error { return nil }

// A write is counted and timed only once the target has accepted the whole
// chunk. forward is driven by hand over a fake target that owns no socket, so a
// write that fails, a short write and a full write are all the test's choice.
// The documented Writes and Shortest claim forwarded traffic: a chunk that did
// not reach the target is not one, and end is called instead. The clock stands
// still but for its waits, so a held chunk's time is exactly the delay and a
// chunk that fails after one succeeded cannot lower Shortest.
func TestFailedForwardDoesNotCountAsSent(t *testing.T) {
	t.Parallel()

	chunk := func(data string, at time.Time) held {
		return held{data: []byte(data), at: at, due: at.Add(delay)}
	}
	for _, c := range []struct {
		name     string
		queue    []held
		steps    []step
		writes   int
		shortest time.Duration
		ended    int64
		offered  []string
	}{
		{
			name:     "a first write that fails counts nothing",
			queue:    []held{chunk("a", epoch), chunk("b", epoch)},
			steps:    []step{{err: errWrite}},
			writes:   0,
			shortest: 0,
			ended:    1,
			offered:  []string{"a"},
		},
		{
			name: "a write that fails after one succeeded keeps only the first",
			queue: []held{
				chunk("a", epoch),
				{data: []byte("b"), at: epoch.Add(delay / 2), due: epoch.Add(delay / 2)},
				chunk("c", epoch),
			},
			steps:    []step{{}, {err: errWrite}},
			writes:   1,
			shortest: delay,
			ended:    1,
			offered:  []string{"a", "b"},
		},
		{
			name:     "a short write is refused",
			queue:    []held{chunk("abc", epoch)},
			steps:    []step{{short: true}},
			writes:   0,
			shortest: 0,
			ended:    1,
			offered:  []string{"abc"},
		},
		{
			name:     "a full chunk is counted once the target accepts it",
			queue:    []held{chunk("a", epoch), chunk("b", epoch)},
			steps:    []step{{}, {}},
			writes:   2,
			shortest: delay,
			ended:    0,
			offered:  []string{"a", "b"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			now := epoch
			p := &Proxy{delay: delay, clock: Clock{
				Now: func() time.Time { return now },
				Wait: func(_ <-chan struct{}, d time.Duration) bool {
					now = now.Add(d)
					return true
				},
			}}
			p.shortest.Store(noHold)

			queue := make(chan held, len(c.queue))
			for _, h := range c.queue {
				queue <- h
			}
			close(queue)

			var ended atomic.Int64
			target := &fakeTarget{steps: c.steps}
			p.forward(target, queue, make(chan struct{}), func() { ended.Add(1) })

			assert.Equal(t, c.writes, p.Writes(), "Writes = %d; want %d", p.Writes(), c.writes)
			assert.Equal(t, c.shortest, p.Shortest(), "Shortest = %v; want %v", p.Shortest(), c.shortest)
			assert.Equal(t, c.ended, ended.Load(), "end was called %d times; want %d", ended.Load(), c.ended)
			assert.Equal(t, c.offered, target.offered(), "the target was offered %q; want %q", target.offered(), c.offered)
		})
	}
}
