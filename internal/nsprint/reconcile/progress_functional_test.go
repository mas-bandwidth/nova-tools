//go:build functional

package reconcile_test

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// The DONE-WHEN's ask half of nova-tools #4319, on a throwaway redis-server
// with the clock injected: a stream with a card nothing takes (a
// deliberately unfixable card: ready, a worker with room, never worked)
// ends with the pit stop set by the system on that stream only, one EVENT
// line, one wake note per ask recipient, within the window; the same
// episode never asks twice; the table's record carries the EVENT.
func TestProgressStallSetsThePitStopOnceAndWakes(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-4319"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })

	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	const S, stream = "sprint-4319", "autonomy"
	pipe := c.Pipeline()
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	pipe.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: stream})
	pipe.ZAdd(ctx, "ws:"+stream+":ready", redis.Z{Score: float64(now.Add(-time.Hour).UnixMilli()), Member: "cards-1"})
	pipe.ZAdd(ctx, "ws:"+stream+":landed", redis.Z{Score: float64(now.Add(-2 * time.Hour).UnixMilli()), Member: "cards-0"})
	pipe.SAdd(ctx, "consumers", "bench:b1")
	pipe.HSet(ctx, "bench:b1:desired", "slots", "2")
	pipe.HSet(ctx, "bench:b1:beat", "at", now.UnixMilli())
	pipe.HSet(ctx, "cfg:progress", "ask", "glenn,rowan", "every_s", "1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	clock := now
	p := &reconcile.Progress{Client: c, Out: &out, Now: func() time.Time { return clock }}
	run := func() reconcile.Counts {
		t.Helper()
		// The beat stays live on the injected clock.
		if err := c.HSet(ctx, "bench:b1:beat", "at", clock.UnixMilli()).Err(); err != nil {
			t.Fatal(err)
		}
		cnt, err := p.Run(ctx, l)
		if err != nil {
			t.Fatalf("run at %s: %v\n%s", clock.Format(time.TimeOnly), err, out.String())
		}
		return cnt
	}
	run()
	if !strings.Contains(out.String(), "PROGRESS autonomy left=1 delta=0 ready=1 landed_h=0 retries_h=0 oldest=1h0m0s blocked=0s window=30m0s status=converging") {
		t.Fatalf("first run:\n%s", out.String())
	}
	clock = clock.Add(29 * time.Minute)
	run()
	if st, _ := pitstop.Read(ctx, c, S); st.Set {
		t.Fatalf("stopped before the window: %s", st.Line())
	}
	clock = clock.Add(time.Minute)
	if cnt := run(); cnt.Refused != 0 {
		t.Fatalf("refused %d:\n%s", cnt.Refused, out.String())
	}
	st, err := pitstop.Read(ctx, c, S)
	if err != nil || !st.Set || st.By != "progress" || !st.InScope(stream) || st.Streams == nil {
		t.Fatalf("stop after the window: %+v %v", st, err)
	}
	want := "PROGRESS STALLED autonomy stall blocked=30m0s window=30m0s left=1 ready=1 landed_h=0 retries_h=0"
	if st.Why != want {
		t.Fatalf("why %q\nwant %q", st.Why, want)
	}
	if n := strings.Count(out.String(), "EVENT "+want); n != 1 {
		t.Fatalf("EVENT lines %d:\n%s", n, out.String())
	}
	notes, err := c.XRange(ctx, friend.OutboxKey, "-", "+").Result()
	if err != nil || len(notes) != 2 {
		t.Fatalf("outbox %d %v", len(notes), err)
	}
	for i, to := range []string{"glenn", "rowan"} {
		v := notes[i].Values
		if v["friend"] != to || v["kind"] != "notice" || v["actor"] != "progress" || !strings.Contains(v["detail"].(string), want) {
			t.Fatalf("note %d: %v", i, v)
		}
	}
	top, _ := c.HGetAll(ctx, reconcile.ProgressKey).Result()
	if top["events"] != "1" || !strings.HasPrefix(top["event"], "EVENT "+want) || top["refused"] != "0" {
		t.Fatalf("proc:progress %v", top)
	}
	// Held now: idle, and the episode never asks again.
	clock = clock.Add(time.Hour)
	run()
	if notes, _ := c.XRange(ctx, friend.OutboxKey, "-", "+").Result(); len(notes) != 2 {
		t.Fatalf("asked twice: %d notes", len(notes))
	}
	if !strings.Contains(out.String(), "status=idle") {
		t.Fatalf("held stream not idle:\n%s", out.String())
	}
	// A restart continues the same state: the stored episode is asked.
	fresh := &reconcile.Progress{Client: c, Out: &out, Now: func() time.Time { return clock }}
	if _, err := fresh.Run(ctx, l); err != nil {
		t.Fatal(err)
	}
	if fresh.Last.States[stream].AskedAt == 0 {
		t.Fatalf("restart lost the episode: %+v", fresh.Last.States[stream])
	}
	// A stop already set (a human's, whole sprint) refuses the ask, printed
	// and counted, and the EVENT and the note still go out.
	if _, err := pitstop.Clear(ctx, c, S, "rowan", "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pitstop.Set(ctx, c, S, "rowan", "looking", false, "t2", "other"); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Second)
	run() // pushable again: blocked starts
	clock = clock.Add(30 * time.Minute)
	out.Reset()
	if cnt := run(); cnt.Refused != 1 || !strings.Contains(out.String(), `PROGRESS REFUSED pitstop set sprint=sprint-4319 why="already stopped by=rowan`) {
		t.Fatalf("refused %d:\n%s", cnt.Refused, out.String())
	}
	if notes, _ := c.XRange(ctx, friend.OutboxKey, "-", "+").Result(); len(notes) != 4 {
		t.Fatalf("second episode notes: %d", len(notes))
	}
}

// A duty refusal repeating past cfg:progress refusals asks once for its
// text, scope=all, and the asked text is remembered on proc:progress.
func TestProgressRepeatingRefusalAsksOnce(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-4319b"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	const S = "sprint-4319b"
	pipe := c.Pipeline()
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	pipe.HSet(ctx, "cfg:progress", "refusals", "3", "ask", "glenn")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	p := &reconcile.Progress{Client: c, Out: &out, Now: func() time.Time { return now }, Every: time.Nanosecond}
	p.NotePass(2, []reconcile.Repeat{{Duty: "land", Text: "cfg:land repos unset", Passes: 2}})
	if _, err := p.Run(ctx, l); err != nil {
		t.Fatal(err)
	}
	if st, _ := pitstop.Read(ctx, c, S); st.Set {
		t.Fatalf("asked under the limit: %s", st.Line())
	}
	if top, _ := c.HGetAll(ctx, reconcile.ProgressKey).Result(); top["refused"] != "2" {
		t.Fatalf("the pass's refusals not recorded: %v", top)
	}
	now = now.Add(time.Second)
	p.NotePass(0, []reconcile.Repeat{{Duty: "land", Text: "cfg:land repos unset", Passes: 3}})
	if _, err := p.Run(ctx, l); err != nil {
		t.Fatal(err)
	}
	st, _ := pitstop.Read(ctx, c, S)
	if !st.Set || st.Streams != nil || st.Why != `PROGRESS STALLED sprint refusal duty=land repeats=3 limit=3 text="cfg:land repos unset"` {
		t.Fatalf("stop: %+v", st)
	}
	now = now.Add(time.Second)
	p.NotePass(0, []reconcile.Repeat{{Duty: "land", Text: "cfg:land repos unset", Passes: 4}})
	if _, err := p.Run(ctx, l); err != nil {
		t.Fatal(err)
	}
	if notes, _ := c.XRange(ctx, friend.OutboxKey, "-", "+").Result(); len(notes) != 1 {
		t.Fatalf("asked twice for one text: %d notes", len(notes))
	}
}

// roundTrips counts every command and pipeline sent to Redis.
type roundTrips struct{ n atomic.Int64 }

func (r *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }
func (r *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { r.n.Add(1); return next(ctx, cmd) }
}
func (r *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { r.n.Add(1); return next(ctx, cmds) }
}

// The cold read of #4361, item 2: the every_s gate comes before any read.
// A pass loop at 1 s on the injected clock with every_s=10 costs three round
// trips per 10 s (the index, the measurement, the record; the pass puts the
// holds on ctx), and a gated pass none.
func TestProgressGatesBeforeAnyRead(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-4361"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	pipe := c.Pipeline()
	pipe.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "autonomy"})
	pipe.ZAdd(ctx, "ws:autonomy:ready", redis.Z{Score: float64(now.UnixMilli()), Member: "cards-1"})
	pipe.HSet(ctx, "cfg:progress", "every_s", "10")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// One connection, dialed before counting: the handshake is not a trip.
	counted := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 1})
	t.Cleanup(func() { _ = counted.Close() })
	if err := counted.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	var trips roundTrips
	counted.AddHook(&trips)
	clock := now
	p := &reconcile.Progress{Client: counted, Now: func() time.Time { return clock }}
	held := pitstop.WithHolds(ctx, nil) // what the pass puts on ctx
	runs := 0
	for pass := 0; pass < 30; pass++ {
		before := trips.n.Load()
		if _, err := p.Run(held, l); err != nil {
			t.Fatal(err)
		}
		switch d := trips.n.Load() - before; {
		case pass%10 == 0 && d != 3:
			t.Fatalf("pass %d (a run): %d round trips, want 3", pass, d)
		case pass%10 != 0 && d != 0:
			t.Fatalf("pass %d (gated): %d round trips, want 0", pass, d)
		case d > 0:
			runs++
		}
		clock = clock.Add(time.Second)
	}
	if runs != 3 || trips.n.Load() != 9 {
		t.Fatalf("30 s of passes: %d runs, %d round trips; want 3 and 9", runs, trips.n.Load())
	}
}

// The cold read of #4361, item 3: proc:progress refused adds the other
// duties' refusals of the last pass to this run's own, each once. The pass
// sum NotePass gets includes the duty's own refusals of that pass, which
// the run already recorded.
func TestProgressRefusedCountsEachRefusalOnce(t *testing.T) {
	t.Parallel()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-4361b"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	if err := c.HSet(ctx, "cfg:progress", "refusals", "1", "ask", "glenn").Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	p := &reconcile.Progress{Client: c, Now: func() time.Time { return now }, Every: time.Nanosecond}
	refused := func() string {
		t.Helper()
		v, _ := c.HGet(ctx, reconcile.ProgressKey, "refused").Result()
		return v
	}
	// Pass 1: two other refusals and a repeat past the limit; no open
	// sprint, so the ask refuses once: 2 + 1.
	p.NotePass(2, []reconcile.Repeat{{Duty: "land", Text: "cfg:land repos unset", Passes: 1}})
	cnt, err := p.Run(ctx, l)
	if err != nil || cnt.Refused != 1 || refused() != "3" {
		t.Fatalf("pass 1: counts %d err %v refused=%s, want 1 and 3", cnt.Refused, err, refused())
	}
	// The pass's sum is 4 others plus that 1: the record is the 4 others
	// and this run's own (none; the text is asked), never 5.
	p.NotePass(4+cnt.Refused, []reconcile.Repeat{{Duty: "land", Text: "cfg:land repos unset", Passes: 2}})
	now = now.Add(time.Second)
	if cnt, err := p.Run(ctx, l); err != nil || cnt.Refused != 0 || refused() != "4" {
		t.Fatalf("pass 2: counts %d err %v refused=%s, want 0 and 4", cnt.Refused, err, refused())
	}
}
