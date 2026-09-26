package land_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// planClaimed plans one single-member batch and claims attempt 1 on bench/slot; it returns the token.
func planClaimed(t *testing.T, f *landTestFixture, n int, batch, bench, slot string) string {
	t.Helper()
	unit := "gh/mas-bandwidth/nova-tools/" + strconv.Itoa(n)
	head := strings.Repeat(strconv.Itoa(n%10), 40)
	fromTip := "1111111111111111111111111111111111111111"
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-" + strconv.Itoa(n), Head: head, BaseSHA: fromTip,
	}); err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}
	tok, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batch, f.lease, unit+"@"+head, "", "go", fromTip, "in-"+batch)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if res, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, batch, 1, tok, bench, slot); err != nil || res != "OK" {
		t.Fatalf("claim: %s %v", res, err)
	}
	return tok
}

// TestL5ServeSweep is control L5 through land serve's reclaim sweeper (spec 5.6, build B9): a
// live worker is left alone; SIGKILL (the heartbeat key gone) is a DEAD line and a requeue on the
// next tick, well inside 30 s of the kill (15 s TTL + one 2 s tick); the dead worker's late
// receipt is STALE; the new attempt's receipt is the one final receipt; the sweeper's Run loop
// reclaims without a tick call (fails on: the 90 s reclaim; a live gate requeued).
func TestL5ServeSweep(t *testing.T) {
	t.Parallel()

	f := newLandFixture(t, "nova-tools", "dev")
	tok1 := planClaimed(t, f, 501, "batch-l5s", "studio", "slot-1")
	if err := f.client.Set(f.ctx, "worker:studio:slot-1", "batch-l5s:1:"+tok1, 15*time.Second).Err(); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	sw := &land.Sweeper{Client: f.client, Repo: f.repo, Base: f.base}
	if sw.Interval = sw.TickInterval(); sw.Interval != 2*time.Second {
		t.Fatalf("default sweep interval %v, want 2s", sw.Interval)
	}
	rep, err := sw.Tick(f.ctx)
	if err != nil || len(rep.Requeued) != 0 || len(rep.Lines) != 0 {
		t.Fatalf("live worker swept: %+v %v", rep, err)
	}

	_ = f.client.Del(f.ctx, "worker:studio:slot-1").Err() // SIGKILL: the key expires
	rep, err = sw.Tick(f.ctx)
	if err != nil || len(rep.Requeued) != 1 || rep.Requeued[0].Attempt != 2 {
		t.Fatalf("dead worker not requeued: %+v %v", rep, err)
	}
	if want := "DEAD bbatch-l5s worker=studio/slot-1 attempt=2"; len(rep.Lines) != 1 || rep.Lines[0] != want {
		t.Fatalf("lines %q, want %q", rep.Lines, want)
	}
	if st, _ := f.client.HGet(f.ctx, land.BatchKey(f.repo, f.base, "batch-l5s"), "state").Result(); st != "queued" {
		t.Fatalf("state after requeue %q", st)
	}
	if rep, _ = sw.Tick(f.ctx); len(rep.Requeued) != 0 {
		t.Fatalf("second tick requeued again: %+v", rep)
	}

	late, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l5s", 1, tok1, "GREEN", "studio", "w1", "th", "tt", "in-batch-l5s", "", "", "", "", "10")
	if err != nil || late != "STALE" {
		t.Fatalf("late receipt %s %v, want STALE", late, err)
	}
	tok2 := rep0Token(t, f, "batch-l5s")
	if res, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l5s", 2, tok2, "superman", "slot-1"); err != nil || res != "OK" {
		t.Fatalf("claim 2: %s %v", res, err)
	}
	if res, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l5s", 2, tok2, "GREEN", "superman", "w2", "th", "tt", "in-batch-l5s", "", "", "", "", "10"); err != nil || res != "OK" {
		t.Fatalf("final receipt %s %v", res, err)
	}
	n1, _ := f.client.Exists(f.ctx, land.ReceiptKey(f.repo, "batch-l5s", 1)).Result()
	n2, _ := f.client.Exists(f.ctx, land.ReceiptKey(f.repo, "batch-l5s", 2)).Result()
	if n1 != 0 || n2 != 1 {
		t.Fatalf("receipts r1=%d r2=%d, want exactly the final one", n1, n2)
	}

	// The Run loop reclaims on its own ticks.
	planClaimed(t, f, 502, "batch-l5r", "studio", "slot-2")
	_ = f.client.Del(f.ctx, "worker:studio:slot-2").Err() // SIGKILL
	var out strings.Builder
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	run := &land.Sweeper{Client: f.client, Repo: f.repo, Base: f.base, Interval: 5 * time.Millisecond, Log: &syncWriter{b: &out}}
	done := make(chan error, 1)
	go func() { done <- run.Run(ctx) }()
	if !pollUntil(func() bool {
		st, _ := f.client.HGet(f.ctx, land.BatchKey(f.repo, f.base, "batch-l5r"), "state").Result()
		return st == "queued"
	}) {
		t.Fatalf("Run never requeued batch-l5r")
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
}

func rep0Token(t *testing.T, f *landTestFixture, batch string) string {
	t.Helper()
	tok, err := f.client.HGet(f.ctx, land.BatchKey(f.repo, f.base, batch), "token").Result()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

// TestL20 is control L20's benching half (spec 8.3, build B9; the budget half is TestL20b*): an
// ERROR, or a RED another bench gates GREEN with the same input_id (in either order), is a bad
// gate; a good gate resets the run; three bad in a row set benched with a BENCHED event, and a
// benched bench is refused a gate; only --reinstate after a bench-conform PASS younger than
// 15 min brings it back (fails on: a sick bench gating on; a reinstate on a stale or failed conform).
func TestL20(t *testing.T) {
	t.Parallel()

	f := newLandFixture(t, "nova-tools", "dev")
	ctx := f.ctx
	gate := func(bench, verdict, input, batch string) land.GateOutcome {
		t.Helper()
		out, err := land.RecordGate(ctx, f.client, f.repo, bench, verdict, input, batch)
		if err != nil {
			t.Fatalf("record %s %s: %v", bench, verdict, err)
		}
		return out
	}
	benched := func(bench string) bool {
		t.Helper()
		b, err := land.IsBenched(ctx, f.client, bench)
		if err != nil {
			t.Fatalf("benched: %v", err)
		}
		return b
	}

	if o := gate("sick", "ERROR", "in-1", "b1"); !o.Bad || o.ConsecutiveBad != 1 || len(o.Benched) != 0 {
		t.Fatalf("ERROR: %+v", o)
	}
	if o := gate("sick", "GREEN", "in-2", "b2"); o.Bad || o.ConsecutiveBad != 0 {
		t.Fatalf("GREEN did not reset: %+v", o)
	}
	gate("sick", "ERROR", "in-3", "b3")
	if o := gate("good", "GREEN", "in-4", "b4"); o.Bad {
		t.Fatalf("good GREEN bad: %+v", o)
	}
	if o := gate("sick", "RED", "in-4", "b4"); !o.Bad || o.ConsecutiveBad != 2 {
		t.Fatalf("RED disputed by a GREEN elsewhere: %+v", o)
	}
	if benched("sick") {
		t.Fatalf("benched after two")
	}
	o := gate("sick", "ERROR", "in-5", "b5")
	if !o.Bad || o.ConsecutiveBad != 3 || len(o.Benched) != 1 || o.Benched[0] != "sick" {
		t.Fatalf("third bad gate did not bench: %+v", o)
	}
	if len(o.Lines) != 1 || o.Lines[0] != "BENCHED sick bad=3 last=b5" {
		t.Fatalf("lines %q", o.Lines)
	}
	if !benched("sick") || benched("good") {
		t.Fatalf("benched sick=%v good=%v", benched("sick"), benched("good"))
	}
	evs, _ := f.client.XRange(ctx, land.EventsStream(f.repo), "-", "+").Result()
	found := false
	for _, e := range evs {
		if e.Values["event"] == "BENCHED" && e.Values["bench"] == "sick" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no BENCHED event")
	}

	// RED first, GREEN elsewhere later: the RED bench takes the bad gate.
	gate("slow", "RED", "in-9", "b9")
	if o := gate("good", "GREEN", "in-9", "b9"); o.Bad || len(o.Charged) != 1 || o.Charged[0] != "slow" {
		t.Fatalf("late GREEN did not charge slow: %+v", o)
	}
	if n, _ := f.client.HGet(ctx, land.BenchLandKey("slow"), "consecutive_bad").Int(); n != 1 {
		t.Fatalf("slow consecutive_bad %d", n)
	}

	// A benched bench is refused a gate, by a named refusal.
	err := land.AdmitBench(ctx, f.client, "sick")
	var ref *land.RefusedError
	if !errors.As(err, &ref) || ref.Reason != land.BenchedReason("sick") {
		t.Fatalf("admit sick: %v", err)
	}
	if err := land.AdmitBench(ctx, f.client, "good"); err != nil {
		t.Fatalf("admit good: %v", err)
	}

	// Reinstate: no conform, a stale PASS, a fresh FAIL are refused; a fresh PASS reinstates.
	now, _ := f.client.Time(ctx).Result()
	ck := "bench:sick:conform"
	for i, setup := range []func(){
		func() {},
		func() { f.client.HSet(ctx, ck, "verdict", "PASS", "at", now.Add(-20*time.Minute).Unix()) },
		func() { f.client.HSet(ctx, ck, "verdict", "FAIL", "at", now.Unix()) },
	} {
		setup()
		err := land.Reinstate(ctx, f.client, f.repo, "sick")
		if !errors.As(err, &ref) || ref.Reason != land.ReinstateReason("sick") {
			t.Fatalf("case %d: reinstate %v, want refused", i, err)
		}
		if !benched("sick") {
			t.Fatalf("case %d: reinstated", i)
		}
	}
	f.client.HSet(ctx, ck, "verdict", "PASS", "at", now.Add(-time.Minute).Unix())
	if err := land.Reinstate(ctx, f.client, f.repo, "sick"); err != nil {
		t.Fatalf("reinstate fresh PASS: %v", err)
	}
	if benched("sick") || land.AdmitBench(ctx, f.client, "sick") != nil {
		t.Fatalf("still benched after reinstate")
	}
	if n, _ := f.client.HGet(ctx, land.BenchLandKey("sick"), "consecutive_bad").Int(); n != 0 {
		t.Fatalf("consecutive_bad %d after reinstate", n)
	}
	if err := land.Reinstate(ctx, f.client, f.repo, "good"); !errors.As(err, &ref) || ref.Reason != land.NotBenchedReason("good") {
		t.Fatalf("reinstate unbenched: %v", err)
	}

	// land bench <b> --out <reason> benches by hand.
	if err := land.BenchOut(ctx, f.client, f.repo, "good", "disk"); err != nil || !benched("good") {
		t.Fatalf("bench out: %v", err)
	}
}

// TestB9StuckAndDeadlines covers spec 5.6's STUCK alarm and 8.6's step deadline (build B9): a
// gate with a live worker claimed longer than 2x its class p99 on that bench prints STUCK once per
// tick and is not requeued; a step killed by its deadline is ERROR, never RED.
func TestB9StuckAndDeadlines(t *testing.T) {
	t.Parallel()

	f := newLandFixture(t, "nova-tools", "dev")
	tok := planClaimed(t, f, 503, "batch-st", "studio", "slot-3")
	_ = f.client.Set(f.ctx, "worker:studio:slot-3", "batch-st:1:"+tok, time.Minute).Err()
	_ = f.client.HSet(f.ctx, land.BenchLandKey("studio"), "p99_ms_go", "1000").Err()
	claimed := int64(1_700_000_000_000)
	_ = f.client.HSet(f.ctx, land.BatchKey(f.repo, f.base, "batch-st"), "claimed_at", strconv.FormatInt(claimed, 10)).Err()

	d, err := land.StepDeadline(f.ctx, f.client, "studio", "go")
	if err != nil || d != 2*time.Second {
		t.Fatalf("deadline %v %v, want 2s", d, err)
	}
	if d, _ := land.StepDeadline(f.ctx, f.client, "nobody", "go"); d != 2*land.DefaultStepP99 {
		t.Fatalf("default deadline %v", d)
	}

	at := claimed
	sw := &land.Sweeper{Client: f.client, Repo: f.repo, Base: f.base, Now: func() time.Time { return time.UnixMilli(at) }}
	at = claimed + 1500
	if rep, _ := sw.Tick(f.ctx); len(rep.Stuck) != 0 {
		t.Fatalf("stuck under the bound: %+v", rep)
	}
	at = claimed + 2500
	rep, err := sw.Tick(f.ctx)
	if err != nil || len(rep.Stuck) != 1 || len(rep.Requeued) != 0 {
		t.Fatalf("stuck: %+v %v", rep, err)
	}
	if want := "STUCK bbatch-st worker=studio/slot-3"; len(rep.Lines) != 1 || !strings.HasPrefix(rep.Lines[0], want) {
		t.Fatalf("lines %q, want %q", rep.Lines, want)
	}

	killed, err := land.RunStepWithDeadline(f.ctx, time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !killed || land.StepVerdict(err, killed) != "ERROR" {
		t.Fatalf("killed=%v verdict=%s", killed, land.StepVerdict(err, killed))
	}
	killed, err = land.RunStepWithDeadline(f.ctx, time.Hour, func(context.Context) error { return errors.New("exit 1") })
	if killed || land.StepVerdict(err, killed) != "RED" {
		t.Fatalf("a failing step: killed=%v verdict=%s", killed, land.StepVerdict(err, killed))
	}
	if killed, err = land.RunStepWithDeadline(f.ctx, time.Hour, func(context.Context) error { return nil }); killed || land.StepVerdict(err, killed) != "GREEN" {
		t.Fatalf("a passing step: %v", killed)
	}
}

// pollUntil polls cond until it holds or NOVA_TEST_WAIT (default 30s) passes: assert the event,
// not the clock.
func pollUntil(cond func() bool) bool {
	bound := 30 * time.Second
	if v, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && v > 0 {
		bound = v
	}
	end := time.Now().Add(bound)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

type syncWriter struct {
	mu sync.Mutex
	b  *strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
