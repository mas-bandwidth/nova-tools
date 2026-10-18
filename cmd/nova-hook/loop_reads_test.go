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

// clockStore is the loop's blocking read on the fake clock, as XREAD BLOCK
// behaves: an entry past the cursor answers at once; with none the read waits
// on the fake clock (the loop's and the store's moving together) until the
// message the test lands at landAt, when that falls inside the block, and
// answers it then, mid-block; else it waits out the whole block. event runs at
// every instant the read wakes at.
type clockStore struct {
	bus.Waiter
	r      *rig
	mu     sync.Mutex
	blocks []time.Duration
	landAt time.Time
	land   func()
	woke   time.Duration // how far into its block the read that landed the message woke
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
	wait := block
	landing := !s.landAt.IsZero() && s.landAt.After(s.r.now()) && !s.landAt.After(s.r.now().Add(block))
	if landing {
		wait = s.landAt.Sub(s.r.now())
	}
	s.r.advance(wait)
	s.r.h.Pass(wait)
	if landing {
		s.land()
		s.landAt, s.woke = time.Time{}, wait
	}
	s.event(s.r.now())
	return st.Range(ctx, stream, "("+after, "+", count)
}

// TestTheLoopReadsItsRecordWhenDueAndKeepsEveryDeadline pins finding 3 of
// 2026-10-17: every loop read its whole record every tick (15 commands a
// second: GET 14.2M and PTTL 14.1M of the bus Redis's 38.3M). The loop now
// blocks until its next renewal or challenge falls due (at most IdleTicks
// ticks) and reads the record at a renewal and after a batch off its stream.
// On the fake clock, at the production tick, over ten minutes: the lease is
// renewed every RenewEvery and never lapses; a challenge landing mid-block
// wakes the read at that instant and is shown then, inside its AnswerBound;
// every read's deadline on a store that hangs (CallTimeout + block +
// BlockMargin) is under LaunchdExit, so a stop is in time; and the store is
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
	landAt := start.Add(4*time.Minute + 7*time.Second + 500*time.Millisecond) // on no block's end
	var shownAt time.Time
	var lapsed []string
	var commands0 int
	cs := &clockStore{r: r, landAt: landAt}
	cs.land = func() {
		b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
		_, err := b.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "HOOK CHALLENGE nonce=feedface", Body: "prove"})
		require.NoError(t, err)
	}
	cs.event = func(now time.Time) {
		for _, p := range r.h.State(t, "ada").Pushes {
			if p.Push == hook.Bus && p.State == hook.PushDown {
				lapsed = append(lapsed, now.Sub(start).String())
			}
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
	assert.Equal(t, landAt, shownAt, "shown at the instant it landed")
	assert.Greater(t, cs.woke, time.Duration(0))
	assert.Less(t, cs.woke, IdleTicks*Tick, "the read woke mid-block, not at its end")
	for _, b := range cs.blocks {
		assert.LessOrEqual(t, b, IdleTicks*Tick, "no read waits past IdleTicks ticks")
		assert.GreaterOrEqual(t, b, Tick, "nor under a tick")
		assert.Less(t, bus.CallTimeout+b+bus.BlockMargin, LaunchdExit, "a read's deadline on a hanging store is inside launchd's wait")
	}
	assert.GreaterOrEqual(t, elapsed, 10*time.Minute)
	perSecond := float64(commands) / elapsed.Seconds()
	assert.Less(t, perSecond, 1.5, "%d commands in %s, where every tick's read was 16 a second", commands, elapsed)
	t.Logf("the loop sent %d commands in %s: %.2f a second", commands, elapsed, perSecond)
}

// TestTheBlockIsBoundedForAStopOnAHangingStore pins blockFor on the fake clock
// for every deadline the loop can hold: far off, near, past; with the
// challenge owned by the server or not. The block is at least a tick and at
// most IdleTicks ticks, and its read's whole deadline on a store that hangs
// (go-redis ends a blocking read at its context's deadline, not at a cancel:
// CallTimeout + block + BlockMargin) stays under LaunchdExit.
func TestTheBlockIsBoundedForAStopOnAHangingStore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	w.tick = Tick
	now := r.now()
	for _, renew := range []time.Duration{-time.Minute, 0, 300 * time.Millisecond, 2 * time.Second, hook.RenewEvery} {
		for _, owned := range []bool{true, false} {
			b := w.blockFor(owned, now.Add(renew), now.Add(hook.ChallengeEvery))
			assert.GreaterOrEqual(t, b, Tick)
			assert.LessOrEqual(t, b, IdleTicks*Tick)
			if renew >= Tick && renew <= IdleTicks*Tick {
				assert.Equal(t, renew, b, "it wakes for the renewal")
			}
			assert.Less(t, bus.CallTimeout+b+bus.BlockMargin, LaunchdExit)
		}
	}
	assert.Equal(t, 2*time.Second, w.blockFor(false, now.Add(hook.RenewEvery), now.Add(2*time.Second)), "and for its own challenge")
	assert.Less(t, bus.CallTimeout+Tick+bus.BlockMargin, LaunchdExit, "a tick's read, with a core or lanes, too")
}

// TestALanesLoopReadsATickAtATimeAndARecordAtMostARenewalOld pins the loop of
// a service with lanes (and so with its config pass every second): its read
// still blocks a tick, and the record its lanes are asked by is the one read
// at the last renewal, after the last batch off the stream or after its own
// challenge: at most RenewEvery (30 s) old. A proof written with nothing on
// the stream (nova-hook prove) is shown at the next renewal, inside 30 s and a
// tick, never later.
func TestALanesLoopReadsATickAtATimeAndARecordAtMostARenewalOld(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.MR.SetTime(r.now())
	w := r.world()
	w.tick = Tick
	getenv := w.getenv
	w.getenv = func(k string) string {
		if k == serviceRunEnv {
			return "1"
		}
		return getenv(k)
	}
	w.home = func() (string, error) { return r.home, nil }
	w.sprint = func(context.Context, string, []string) (string, error) { return "", context.DeadlineExceeded }
	w.novaConfig = func(context.Context, []string) (string, error) {
		return `{"result":{"status":"ok"},"items":[{"kind":"friend","fields":{"name":"ada","width":"1"}}]}`, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// a service run opens its session under its own signals first: their stop is no stop of the loop
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, func() {} }
	start := r.now()
	var nonce string
	var provedAt, shownAt time.Time
	cs := &clockStore{r: r}
	cs.event = func(now time.Time) {
		if provedAt.IsZero() && nonce != "" && now.Sub(start) >= 47*time.Second {
			r.cli().Do(t, "prove", "--as", "ada", "--nonce", nonce).Exit(0)
			provedAt = now
		}
		if now.Sub(start) >= 3*time.Minute {
			cancel()
		}
	}
	w.waitOn = func(wt bus.Waiter) bus.Waiter { cs.Waiter = wt; return cs }
	out := &streamOut{each: func(l string) {
		if m := nonceRe.FindStringSubmatch(l); m != nil && strings.HasPrefix(l, "HOOK CHALLENGE") {
			nonce = m[1]
		}
		if strings.HasPrefix(l, "HOOK STATE") && strings.Contains(l, "bus=proven") && shownAt.IsZero() {
			shownAt = r.now()
		}
	}}
	var errb strings.Builder
	code := run([]string{"hook", "--as", "ada", "--role", "friend"}, strings.NewReader(""), out, &errb, w)
	require.Zero(t, code, out.all.String()+errb.String())
	require.NotEmpty(t, cs.blocks, out.all.String())
	for _, b := range cs.blocks {
		assert.Equal(t, Tick, b, "a loop with lanes reads a tick at a time")
	}
	require.False(t, provedAt.IsZero(), "the challenge was printed: %s", out.all.String())
	require.False(t, shownAt.IsZero(), "the proof was shown: %s", out.all.String())
	assert.LessOrEqual(t, shownAt.Sub(provedAt), hook.RenewEvery+Tick, "the record read is at most a renewal old")
	assert.Greater(t, shownAt.Sub(provedAt), Tick, "and not read every tick")
}
