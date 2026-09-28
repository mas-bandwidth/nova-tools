package reconcile_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// The progress duty's measurement (nova-tools #4319), on an injected clock:
// convergence is a fall of left inside the window; idle is nothing to push;
// a stall is the whole window pushable with no fall, and an episode asks
// once. No Redis and no wall clock here; the duty's Redis face is
// progress_functional_test.go.

const window = 30 * time.Minute

var t0 = time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

func TestProgressStepConvergesIdlesAndStalls(t *testing.T) {
	t.Parallel()
	s := reconcile.Sample{Stream: "cards", Ready: 20}
	st := reconcile.Step(reconcile.State{}, s, true, t0, window)
	if st.Status != reconcile.StatusConverging || st.Left != 20 || st.BlockedSince != t0.UnixMilli() || st.Delta() != 0 {
		t.Fatalf("first sample: %+v", st)
	}
	// A fall: converging, the blocked clock resets, delta is negative.
	s.Ready = 19
	st = reconcile.Step(st, s, true, t0.Add(10*time.Minute), window)
	if st.Status != reconcile.StatusConverging || st.BlockedSince != 0 || st.Delta() != -1 || st.FellAt != t0.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("after a fall: %+v", st)
	}
	// No fall, pushable: the blocked clock starts now.
	t2 := t0.Add(20 * time.Minute)
	st = reconcile.Step(st, s, true, t2, window)
	if st.Status != reconcile.StatusConverging || st.BlockedSince != t2.UnixMilli() || st.NeedsAsk() {
		t.Fatalf("blocked, inside the window: %+v", st)
	}
	// One second short of the window: still converging.
	st = reconcile.Step(st, s, true, t2.Add(window-time.Second), window)
	if st.Status != reconcile.StatusConverging || st.NeedsAsk() {
		t.Fatalf("one second short: %+v", st)
	}
	// The window: stalled, and the episode needs its one ask.
	t3 := t2.Add(window)
	st = reconcile.Step(st, s, true, t3, window)
	if st.Status != reconcile.StatusStalled || !st.NeedsAsk() || st.Blocked(t3) != window {
		t.Fatalf("at the window: %+v", st)
	}
	// Asked: the same episode never asks again, however long it stalls.
	st.AskedAt = st.BlockedSince
	st = reconcile.Step(st, s, true, t3.Add(time.Hour), window)
	if st.Status != reconcile.StatusStalled || st.NeedsAsk() {
		t.Fatalf("asked episode: %+v", st)
	}
	// A fall ends the episode; a new full window of no fall is a new one.
	s.Ready = 18
	t4 := t3.Add(2 * time.Hour)
	st = reconcile.Step(st, s, true, t4, window)
	if st.Status != reconcile.StatusConverging || st.BlockedSince != 0 {
		t.Fatalf("fall ends the episode: %+v", st)
	}
	st = reconcile.Step(st, s, true, t4.Add(time.Second), window)
	st = reconcile.Step(st, s, true, t4.Add(time.Second+window), window)
	if st.Status != reconcile.StatusStalled || !st.NeedsAsk() {
		t.Fatalf("new episode asks again: %+v", st)
	}
}

func TestProgressStepIdleWhenNothingCanBePushed(t *testing.T) {
	t.Parallel()
	blocked := reconcile.Step(reconcile.State{}, reconcile.Sample{Stream: "s", Ready: 3}, true, t0, window)
	for name, c := range map[string]struct {
		s    reconcile.Sample
		room bool
	}{
		"nothing in play": {reconcile.Sample{Stream: "s", Review: 3}, true},
		"no worker room":  {reconcile.Sample{Stream: "s", Ready: 3}, false},
		"held by a stop":  {reconcile.Sample{Stream: "s", Ready: 3, Working: 1, Held: true}, true},
	} {
		st := reconcile.Step(blocked, c.s, c.room, t0.Add(2*window), window)
		if st.Status != reconcile.StatusIdle || st.BlockedSince != 0 || st.NeedsAsk() {
			t.Errorf("%s: %+v", name, st)
		}
	}
	// Left of zero with nothing to push: idle, never stalled.
	st := reconcile.Step(blocked, reconcile.Sample{Stream: "s", Landed: 3}, true, t0.Add(2*window), window)
	if st.Status != reconcile.StatusIdle || st.Left != 0 {
		t.Fatalf("all landed: %+v", st)
	}
}

// The cold read of #4361, item 1: a card that churns working -> ready ->
// working with no fall kept the stream idle (nothing ready half the time,
// no room the other half), so it never stalled. Working is in play: the
// blocked clock runs through the churn and the stream stalls at the window,
// the samples 10 s apart on the injected clock.
func TestProgressChurnWithNoFallStallsWithinTheWindow(t *testing.T) {
	t.Parallel()
	working := reconcile.Sample{Stream: "s", Working: 1, RetriesHour: 3}
	ready := reconcile.Sample{Stream: "s", Ready: 1, RetriesHour: 3}
	var st reconcile.State
	var stalledAt time.Duration
	for at := time.Duration(0); at <= window; at += 10 * time.Second {
		s, room := working, false // the one worker's slot is the churning card's
		if (at/(10*time.Second))%2 == 1 {
			s, room = ready, true // back in ready: the slot is free again
		}
		st = reconcile.Step(st, s, room, t0.Add(at), window)
		if st.Status == reconcile.StatusIdle {
			t.Fatalf("churn read as idle at %s: %+v", at, st)
		}
		if st.Status == reconcile.StatusStalled && stalledAt == 0 {
			stalledAt = at
		}
	}
	if stalledAt != window || !st.NeedsAsk() || st.Blocked(t0.Add(window)) != window {
		t.Fatalf("stalled at %s, want %s: %+v", stalledAt, window, st)
	}
	// Every card working with no room and no fall: in play, stalls too.
	all := reconcile.Step(reconcile.State{}, reconcile.Sample{Stream: "w", Working: 4}, false, t0, window)
	all = reconcile.Step(all, reconcile.Sample{Stream: "w", Working: 4}, false, t0.Add(window), window)
	if all.Status != reconcile.StatusStalled {
		t.Fatalf("all working, no fall: %+v", all)
	}
	// A fall still resets the clock mid-churn.
	st = reconcile.Step(st, reconcile.Sample{Stream: "s"}, true, t0.Add(window+10*time.Second), window)
	if st.BlockedSince != 0 || st.Status != reconcile.StatusIdle {
		t.Fatalf("the card landed: %+v", st)
	}
}

func TestProgressWindowRollsTheDeltaReference(t *testing.T) {
	t.Parallel()
	st := reconcile.Step(reconcile.State{}, reconcile.Sample{Stream: "s", Ready: 10}, true, t0, window)
	// Cards cut into the stream inside the window: delta is positive.
	st = reconcile.Step(st, reconcile.Sample{Stream: "s", Ready: 14}, true, t0.Add(time.Minute), window)
	if st.Delta() != 4 {
		t.Fatalf("cut: delta %d", st.Delta())
	}
	// The window passes: the reference rolls to now.
	st = reconcile.Step(st, reconcile.Sample{Stream: "s", Ready: 14}, true, t0.Add(window), window)
	if st.Delta() != 0 || st.RefAt != t0.Add(window).UnixMilli() {
		t.Fatalf("rolled: %+v", st)
	}
}

func TestProgressDecideAsksOncePerEpisodeAndShape(t *testing.T) {
	t.Parallel()
	cfg := reconcile.ParseProgressConfig(nil)
	now := t0.Add(window)
	stalled := reconcile.Step(reconcile.Step(reconcile.State{}, reconcile.Sample{Stream: "a", Ready: 2}, true, t0, window),
		reconcile.Sample{Stream: "a", Ready: 2}, true, now, window)
	samples := []reconcile.Sample{{Stream: "a", Ready: 2, LandedHour: 1}, {Stream: "b", Ready: 1}}
	states := map[string]reconcile.State{"a": stalled, "b": reconcile.Step(reconcile.State{}, samples[1], true, now, window)}
	asks := reconcile.Decide(samples, states, nil, nil, 0, "", cfg, now)
	if len(asks) != 1 || asks[0].Stream != "a" || asks[0].Cause != "stall" {
		t.Fatalf("asks %+v", asks)
	}
	want := "PROGRESS STALLED a stall blocked=30m0s window=30m0s left=2 ready=2 landed_h=1 retries_h=0"
	if asks[0].Why() != want {
		t.Fatalf("why %q\nwant %q", asks[0].Why(), want)
	}
	if ev := asks[0].Event("sprint-1", now); ev != "EVENT "+want+" sprint=sprint-1 at="+itoa(now.UnixMilli()) {
		t.Fatalf("event %q", ev)
	}
	// Asked: no ask.
	stalled.AskedAt = stalled.BlockedSince
	states["a"] = stalled
	if asks := reconcile.Decide(samples, states, nil, nil, 0, "", cfg, now); len(asks) != 0 {
		t.Fatalf("asked episode asks again: %+v", asks)
	}
	// A duty refusal repeating: under the limit no ask, at it one, asked none.
	rep := []reconcile.Repeat{{Duty: "land", Text: "cfg:land repos unset", Passes: cfg.Refusals - 1}}
	if asks := reconcile.Decide(nil, nil, rep, nil, 0, "", cfg, now); len(asks) != 0 {
		t.Fatalf("under the limit: %+v", asks)
	}
	rep[0].Passes = cfg.Refusals
	asks = reconcile.Decide(nil, nil, rep, nil, 0, "", cfg, now)
	if len(asks) != 1 || asks[0].Stream != "" || asks[0].Cause != "refusal" ||
		asks[0].Why() != `PROGRESS STALLED sprint refusal duty=land repeats=20 limit=20 text="cfg:land repos unset"` {
		t.Fatalf("at the limit: %+v", asks)
	}
	if asks := reconcile.Decide(nil, nil, rep, map[string]string{"land": rep[0].Text}, 0, "", cfg, now); len(asks) != 0 {
		t.Fatalf("asked shape asks again: %+v", asks)
	}
	// A release probe failing twice asks once.
	asks = reconcile.Decide(nil, nil, nil, nil, 2, "studio v12 no beat", cfg, now)
	if len(asks) != 1 || asks[0].Cause != "release-probe" || !strings.Contains(asks[0].Why(), "fails=2 limit=2") {
		t.Fatalf("probe: %+v", asks)
	}
	if asks := reconcile.Decide(nil, nil, nil, map[string]string{"release-probe": "studio v12 no beat"}, 2, "studio v12 no beat", cfg, now); len(asks) != 0 {
		t.Fatalf("asked probe asks again: %+v", asks)
	}
}

func TestProgressLineAndConfig(t *testing.T) {
	t.Parallel()
	cfg := reconcile.ParseProgressConfig(map[string]string{"window_s": "600", "refusals": "5", "every_s": "x", "ask": " glenn , "})
	if cfg.Window != 10*time.Minute || cfg.Refusals != 5 || cfg.Every != reconcile.DefaultProgressEvery || len(cfg.Ask) != 1 || cfg.Ask[0] != "glenn" {
		t.Fatalf("config %+v", cfg)
	}
	def := reconcile.ParseProgressConfig(nil)
	if def.Window != 30*time.Minute || def.Refusals != 20 || def.Every != 10*time.Second || strings.Join(def.Ask, ",") != "glenn,rowan" {
		t.Fatalf("defaults %+v", def)
	}
	s := reconcile.Sample{Stream: "cards", Ready: 3, Working: 2, Landed: 5, LandedHour: 4, RetriesHour: 1, OldestAt: t0.Add(-90 * time.Second).UnixMilli()}
	st := reconcile.Step(reconcile.State{}, s, true, t0, window)
	got := reconcile.ProgressLine(s, st, t0.Add(5*time.Second), window)
	want := "PROGRESS cards left=5 delta=0 ready=3 landed_h=4 retries_h=1 oldest=1m35s blocked=5s window=30m0s status=converging"
	if got != want {
		t.Fatalf("line %q\nwant %q", got, want)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
