package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clockStore is the loop's blocking read on the fake clock:
// an entry past the cursor answers at once; with none the read waits out its
// whole block, the loop's clock and the store's moving on together, then the
// test's event for that instant runs and the read answers what landed.
type clockStore struct {
	bus.Waiter
	r      *rig
	mu     sync.Mutex
	blocks []time.Duration
	event  func(now time.Time)
	own    int // the commands the fake read and the test's events sent, not the loop's
}

func (s *clockStore) BlockRead(ctx context.Context, stream, after string, block time.Duration, count int) ([]bus.Entry, error) {
	n0 := s.r.h.MR.CommandCount()
	defer func() { s.own += s.r.h.MR.CommandCount() - n0 - 1 }() // the one read the loop asked for is its own
	st := bus.Redis{C: s.r.h.Client}
	if es, err := st.Range(ctx, stream, "("+after, "+", count); err != nil || len(es) > 0 {
		return es, err
	}
	s.mu.Lock()
	s.blocks = append(s.blocks, block)
	s.mu.Unlock()
	s.r.advance(block)
	s.r.h.Pass(block)
	s.event(s.r.now())
	return st.Range(ctx, stream, "("+after, "+", count)
}

// TestTheLoopReadsItsRecordWhenDueAndKeepsEveryDeadline pins finding 3 of
// 2026-10-17: every loop read its whole record every tick (15 commands a
// second: GET 14.2M and PTTL 14.1M of the bus Redis's 38.3M). The loop now
// blocks until its next renewal or challenge falls due (at most IdleTicks
// ticks) and reads the record at a renewal and after a batch off its stream.
// On the fake clock, at the production tick, over ten minutes: the lease is
// renewed every RenewEvery and never lapses, a challenge sent to the stream is
// shown at the instant it lands (inside its AnswerBound), and the store is
// sent a small fraction of the old 15 commands a second.
func TestTheLoopReadsItsRecordWhenDueAndKeepsEveryDeadline(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.MR.SetTime(r.now())
	w := r.world()
	w.tick = Tick // the production tick: every wait is on the fake clock
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	start := r.now()
	challengeAt := start.Add(4*time.Minute + 7*time.Second)
	var shownAt, landedAt time.Time
	var mu sync.Mutex
	var lapsed []string
	var commands0 int
	cs := &clockStore{r: r}
	cs.event = func(now time.Time) {
		st := r.h.State(t, "ada")
		for _, p := range st.Pushes {
			if p.Push == hook.Bus && p.State == hook.PushDown {
				mu.Lock()
				lapsed = append(lapsed, now.Sub(start).String())
				mu.Unlock()
			}
		}
		if !now.Before(challengeAt) && !challengeAt.IsZero() {
			b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
			_, err := b.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "HOOK CHALLENGE nonce=feedface", Body: "prove"})
			require.NoError(t, err)
			challengeAt, landedAt = time.Time{}, now
		}
		if now.Sub(start) >= 10*time.Minute {
			cancel()
		}
	}
	w.waitOn = func(wt bus.Waiter) bus.Waiter { cs.Waiter = wt; return cs }
	out := &streamOut{each: func(l string) {
		if strings.HasPrefix(l, "HOOK ARMED") {
			commands0 = r.h.MR.CommandCount()
		}
		if strings.HasPrefix(l, "HOOK PUSH") && strings.Contains(l, "feedface") {
			shownAt = r.now()
		}
	}}
	var errb strings.Builder
	code := run([]string{"hook", "--as", "ada", "--role", "comms"}, strings.NewReader(""), out, &errb, w)
	require.Zero(t, code, out.all.String()+errb.String())
	elapsed := r.now().Sub(start)
	commands := r.h.MR.CommandCount() - commands0 - cs.own

	assert.Empty(t, lapsed, "the bus lease never lapsed")
	require.False(t, shownAt.IsZero(), "the challenge was shown: %s", out.all.String())
	assert.Equal(t, landedAt, shownAt, "shown at the instant it landed, inside its AnswerBound")
	for _, b := range cs.blocks {
		assert.LessOrEqual(t, b, IdleTicks*Tick, "no read waits past IdleTicks ticks")
		assert.GreaterOrEqual(t, b, Tick, "nor under a tick")
	}
	assert.GreaterOrEqual(t, elapsed, 10*time.Minute)
	perSecond := float64(commands) / elapsed.Seconds()
	assert.Less(t, perSecond, 1.0, "%d commands in %s, where every tick's read was 16 a second", commands, elapsed)
	t.Logf("the loop sent %d commands in %s: %.2f a second", commands, elapsed, perSecond)
}
