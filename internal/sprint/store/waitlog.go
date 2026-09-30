package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The run loop wakes on the log, not the clock (the owner's finding of
// 2026-09-30: "these gaps of 1sec are pernicious"; the design's clock, v2.1
// section 1.4.2's TickEvery, is the quiet log's clock here). Every
// step that changes the sprint commits its lines to the epoch's log; the loop
// blocks on the log from the last line it has seen, and a line wakes it at
// once, so a landing, a finish or a start is ticked on within TickFloor, not
// within TickEvery. A quiet log wakes it at TickEvery: the sweep, the
// presence and the lateness keep their clock.

// TickFloor is the least time between the starts of two ticks of run: a busy
// log ticks at most ten times a second.
const TickFloor = 100 * time.Millisecond

// LogWaiter is a backend that can block on its log.
type LogWaiter interface {
	// WaitLog blocks until the log holds a line after the stream id after
	// ("" is before every line), or for at most d. It returns the log's last
	// stream id as it woke (the cursor for the next wait: every line up to it
	// was committed before the wait returned), and whether a line woke it.
	// It is one exchange with the store.
	WaitLog(ctx context.Context, after string, d time.Duration) (tail string, woke bool, err error)
}

// WaitLog blocks on the log of the epoch until a line after the stream id
// after is there, or for at most d, and returns the cursor for the next wait
// and whether a line woke it. A backend that cannot block waits d by the
// store's clock and says it was not woken.
func (st *Store) WaitLog(ctx context.Context, epoch uint64, after string, d time.Duration) (string, bool, error) {
	root := st.root
	if root == nil {
		root = st.B
	}
	b := root
	if epoch != 0 {
		b = root.AtEpoch(epoch, false)
	}
	if w, ok := b.(LogWaiter); ok {
		return w.WaitLog(ctx, after, d)
	}
	st.sleep(d)
	return after, false, nil
}

// LogTail is the last stream id of the pinned epoch's log ("" when it is
// empty): where a wait that has seen everything so far starts.
func (st *Store) LogTail(ctx context.Context) (string, error) {
	lg, _, err := st.B.Tails(ctx)
	return lg, err
}

// WaitLog is XREAD BLOCK on the log from after, one line, pipelined with a
// read of the log's last id: one round trip. The last id is read after the
// XREAD returns, so every line up to it was committed before the loop's next
// read, and the cursor never skips a line the next tick does not see.
func (r *Redis) WaitLog(ctx context.Context, after string, d time.Duration) (string, bool, error) {
	start := after
	if start == "" {
		start = "0-0"
	}
	// BLOCK 0 is for ever: the least wait is a millisecond
	d = max(d, time.Millisecond)
	p := r.C.Pipeline()
	xr := p.XRead(ctx, &redis.XReadArgs{Streams: []string{r.key(keyLog), start}, Count: 1, Block: d})
	tail := p.XRevRangeN(ctx, r.key(keyLog), "+", "-", 1)
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return after, false, err
	}
	woke := false
	if streams, err := xr.Result(); err == nil {
		for _, s := range streams {
			woke = woke || len(s.Messages) > 0
		}
	}
	last := after
	if ms, err := tail.Result(); err == nil && len(ms) > 0 {
		last = ms[0].ID
	}
	return last, woke, nil
}

// WaitLog is the Mem's wait on its log: a line already after the cursor
// returns at once; else LogWait, when set, is the wait, and else a commit's
// wake or the time, whichever comes first.
func (m *Mem) WaitLog(ctx context.Context, after string, d time.Duration) (string, bool, error) {
	m.mu.Lock()
	m.Calls["waitlog"]++
	tail, woke := m.logAfter(after)
	if woke {
		m.mu.Unlock()
		return tail, true, nil
	}
	if m.logged == nil {
		m.logged = make(chan struct{})
	}
	wake := m.logged
	m.mu.Unlock()
	if m.LogWait != nil {
		m.LogWait(d)
	} else {
		t := time.NewTimer(d)
		select {
		case <-wake:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tail, woke = m.logAfter(after)
	return tail, woke, ctx.Err()
}

// logAfter is the pinned epoch's last line id ("" when none) and whether a
// line lies after the id after. The caller holds m.mu.
func (m *Mem) logAfter(after string) (string, bool) {
	l := m.log()
	if len(l.lines) == 0 {
		return after, false
	}
	tail := l.lines[len(l.lines)-1].id
	return tail, memSeq(tail) > memSeq(after)
}

// memSeq is the number of a Mem stream id ("<n>-0"); "" is 0.
func memSeq(id string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(id, "-0"))
	return n
}

// wakeLog wakes every wait on the log. The caller holds m.mu.
func (m *Mem) wakeLog() {
	if m.logged != nil {
		close(m.logged)
		m.logged = nil
	}
}
