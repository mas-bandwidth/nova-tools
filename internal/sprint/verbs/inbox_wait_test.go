package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The tests of inbox --wait (the owner's ask, 2026-09-30) on the twin: the
// notification stream is a MemStream the test appends to from its own
// goroutine while the wait blocks in another, and time is a clock the test
// moves, so no test waits on the wall clock.

// jrClock is a clock the test moves: After fires once the clock reaches it.
type jrClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []jrTimer
	fired  int
}

type jrTimer struct {
	at time.Time
	ch chan time.Time
}

func newJRClock() *jrClock { return &jrClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)} }

func (c *jrClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *jrClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, jrTimer{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock on and fires every timer it reaches.
func (c *jrClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	var left []jrTimer
	for _, t := range c.timers {
		if t.at.After(c.now) {
			left = append(left, t)
			continue
		}
		t.ch <- c.now
		c.fired++
	}
	c.timers = left
}

func (c *jrClock) Fired() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

// jrWaiter is a wait on a MemStream: the stream, the clock, a signal each time
// a read blocks, and every read's ids.
type jrWaiter struct {
	ms      *MemStream
	clock   *jrClock
	blocked chan struct{}
	mu      sync.Mutex
	froms   [][]StreamAt
}

func newJRWaiter() *jrWaiter {
	w := &jrWaiter{ms: NewMemStream(), clock: newJRClock(), blocked: make(chan struct{}, 64)}
	w.ms.After = w.clock.After
	w.ms.OnBlock = func() { w.blocked <- struct{}{} }
	return w
}

// Read records the ids it reads from and reads the MemStream.
func (w *jrWaiter) Read(ctx context.Context, from []StreamAt, count int, block time.Duration) ([]StreamEntry, error) {
	w.mu.Lock()
	w.froms = append(w.froms, append([]StreamAt(nil), from...))
	w.mu.Unlock()
	return w.ms.Read(ctx, from, count, block)
}

func (w *jrWaiter) wait(timeout time.Duration) *InboxWait {
	return &InboxWait{Notes: w, Timeout: timeout, Now: w.clock.Now}
}

// jrWaitResult is what a wait run in its own goroutine returned.
type jrWaitResult struct {
	res  Result
	view InboxView
	err  error
}

// start runs inbox --wait in its own goroutine.
func (w *jrWaiter) start(env *Env, after uint64, timeout time.Duration) <-chan jrWaitResult {
	done := make(chan jrWaitResult, 1)
	go func() {
		var v InboxView
		res, err := Inbox(context.Background(), env, InboxReq{After: after, Wait: w.wait(timeout), Out: &v})
		done <- jrWaitResult{res: res, view: v, err: err}
	}()
	return done
}

// append adds a note line to an epoch's log, as a step's XADD writes it (L2
// 1.1: id <seq>-0, fields n and d, d the stored body with k "n").
func (w *jrWaiter) append(t *testing.T, epoch uint64, seq, kind, typ string, about ...string) {
	t.Helper()
	w.appendMeta(t, epoch, seq, about, map[string]any{"kind": kind, "type": typ, "cause": "LIMIT", "op": "open", "text": "raised by the test"})
}

// appendTickEnd adds a tick-end note of n to an epoch's log, as the tick's
// last step writes it (errata 3 amendment 8).
func (w *jrWaiter) appendTickEnd(t *testing.T, epoch uint64, seq string, n int) {
	t.Helper()
	te := sprint.TickEndNote(n)
	w.appendMeta(t, epoch, seq, te.Subjects, map[string]any{"kind": sprint.TickEnd, "type": te.Type, "op": te.Op, "text": te.Text, "to": sprint.TickEndTo})
}

func (w *jrWaiter) appendMeta(t *testing.T, epoch uint64, seq string, about []string, meta map[string]any) {
	t.Helper()
	d, err := json.Marshal(map[string]any{"k": "n", "ms": "1790000000000", "about": about, "meta": meta})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ms.Append(logKey(jrPrefix, epoch), seq+"-0", map[string]string{"n": "0", "d": string(d)}); err != nil {
		t.Fatal(err)
	}
}

// TestInboxWaitWakesOnTheTickEnd: a wait blocked on the stream reads past the
// judgments, the DECIDED and the HAPPENED lines, blocking again after each,
// and returns on the tick-end note that follows them, with the tick's count,
// Cursor where the following inbox reads the batch from and Last the tick-end's
// seq. The stream's clock has not moved when it returns: it woke on the append,
// with nothing of its block spent (the store's test measures the wall-clock
// wake). The read before the wait is one round trip; the blocked reads are none.
func TestInboxWaitWakesOnTheTickEnd(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	wt := newJRWaiter()
	w.cc.Reset()
	done := wt.start(w.env, 0, time.Hour)
	<-wt.blocked
	wt.append(t, 0, "1", sprint.Judgment, typeStepRefused, "deal")
	<-wt.blocked
	wt.append(t, 0, "2", sprint.Decided, typeStepRefused, "deal")
	<-wt.blocked
	wt.append(t, 0, "3", sprint.Happened, sprint.NBatchLanded, "p9")
	<-wt.blocked
	wt.append(t, 0, "4", sprint.Judgment, typeStepRefused, "sweep")
	<-wt.blocked
	at := wt.clock.Now()
	wt.appendTickEnd(t, 0, "5", 2)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !wt.clock.Now().Equal(at) || wt.clock.Fired() != 0 {
		t.Fatalf("the wait returned with the clock moved or a block run out: %d fired", wt.clock.Fired())
	}
	if !r.view.Woke || r.view.Judgments != 2 || r.view.Cursor != "0" || r.view.Last != "5" || len(r.view.Groups) != 0 {
		t.Fatalf("the wait returned %+v, want the tick-end n5 of 2, cursor 0, last 5", r.view)
	}
	if !strings.Contains(r.res.Said, "INBOX WAIT tick-end=n5 judgments=2 cursor=0 last=5") {
		t.Fatalf("the wait said %q", r.res.Said)
	}
	if r.res.Trips != 1 || w.cc.Trips() != 1 || w.cc.Steps() != 0 {
		t.Fatalf("the wait made %d round trips (counted %d) and %d steps, want the one read", r.res.Trips, w.cc.Trips(), w.cc.Steps())
	}
}

// TestInboxWaitTimesOut: with no tick-end by --timeout the wait returns
// nothing, however many judgments it read past, and consumes nothing: Last is
// the cursor as given, so the next wait finds the tick-end that covers them.
func TestInboxWaitTimesOut(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	wt := newJRWaiter()
	done := wt.start(w.env, 0, 5*time.Minute)
	<-wt.blocked
	wt.append(t, 0, "1", sprint.Judgment, typeStepRefused, "deal")
	<-wt.blocked
	wt.clock.Advance(5 * time.Minute)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.view.Woke || len(r.view.Groups) != 0 || r.view.Last != "0" || r.view.Cursor != "0" || !strings.Contains(r.res.Said, "INBOX WAIT nothing") {
		t.Fatalf("the wait at its timeout returned %+v, said %q; want nothing, last 0", r.view, r.res.Said)
	}
}

// TestInboxWaitCursor: the wait reads from the cursor, the last seq the
// coordinator has read: a tick-end appended after it before the wait began is
// returned at once, with no block; one at or before it is not returned again.
// A cursor past the log's last line (another epoch's) is refused before any
// block.
func TestInboxWaitCursor(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.open(typeStepRefused, "LIMIT", "k1") // the twin's log has a line 1 or more
	wt := newJRWaiter()
	wt.appendTickEnd(t, 0, "1", 1)
	var v InboxView
	if _, err := Inbox(context.Background(), w.env, InboxReq{Wait: wt.wait(time.Hour), Out: &v}); err != nil {
		t.Fatal(err)
	}
	if !v.Woke || v.Last != "1" || v.Judgments != 1 || len(wt.blocked) != 0 {
		t.Fatalf("a tick-end after the cursor: %+v, %d blocks; want n1 at once", v, len(wt.blocked))
	}
	wt.appendTickEnd(t, 0, "2", 3)
	if _, err := Inbox(context.Background(), w.env, InboxReq{After: 1, Wait: wt.wait(time.Hour), Out: &v}); err != nil {
		t.Fatal(err)
	}
	if !v.Woke || v.Last != "2" || v.Cursor != "1" || v.Judgments != 3 || len(wt.blocked) != 0 {
		t.Fatalf("from cursor 1: %+v, %d blocks; want n2 at once", v, len(wt.blocked))
	}
	_, err := Inbox(context.Background(), w.env, InboxReq{After: 1 << 40, Wait: wt.wait(time.Hour)})
	var rf *Refused
	if !errors.As(err, &rf) || !rf.Local || !strings.Contains(err.Error(), "past the log's last line") || len(wt.blocked) != 0 {
		t.Fatalf("a cursor past the log: %v, want a local refusal before any block", err)
	}
}

// TestInboxWaitResumesFromTheCursor: a read whose connection is lost is read
// again from where the wait had read to, not from where it began: the line
// read before is not read twice and a tick-end appended across the loss is
// returned. Lost past waitLost times in a row, the wait gives up with the loss.
func TestInboxWaitResumesFromTheCursor(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	wt := newJRWaiter()
	done := wt.start(w.env, 0, time.Hour)
	<-wt.blocked
	wt.append(t, 0, "1", sprint.Judgment, typeStepRefused, "deal")
	<-wt.blocked
	wt.ms.Lose(1)
	<-wt.blocked
	wt.appendTickEnd(t, 0, "2", 1)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !r.view.Woke || r.view.Last != "2" || r.view.Cursor != "0" {
		t.Fatalf("across a lost connection the wait returned %+v, want the tick-end n2", r.view)
	}
	wt.mu.Lock()
	froms := wt.froms
	wt.mu.Unlock()
	if len(froms) != 3 || froms[0][0].After != "0-0" || froms[1][0].After != "1-0" || froms[2][0].After != "1-0" {
		t.Fatalf("the reads were from %v, want 0-0, then 1-0 before and after the loss", froms)
	}

	lost := newJRWaiter()
	lost.ms.Lose(waitLost + 1)
	_, err := Inbox(context.Background(), w.env, InboxReq{Wait: lost.wait(time.Hour)})
	var sl *StreamLost
	if !errors.As(err, &sl) {
		t.Fatalf("a wait whose connection is lost %d times: %v, want the loss", waitLost+1, err)
	}
}

// TestInboxWaitFollowsAClear: the wait reads the next epoch's log beside its
// own, so a tick-end written after a clear during the wait is returned, its id
// of the new epoch, and the following inbox reads the new epoch from its start.
func TestInboxWaitFollowsAClear(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	wt := newJRWaiter()
	done := wt.start(w.env, 0, time.Hour)
	<-wt.blocked
	wt.appendTickEnd(t, 1, "1", 1)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !r.view.Woke || r.view.Last != "1" || r.view.Cursor != "0" || r.res.Epoch != 1 || !strings.Contains(r.res.Said, "tick-end=n1~1") {
		t.Fatalf("a tick-end of the next epoch: %+v, epoch %d, said %q; want n1~1 at epoch 1", r.view, r.res.Epoch, r.res.Said)
	}
}
