package sprint

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The tests of the position rules (IT08, R3, R4, R5, R15, R19), against the twin
// of rules_position_twin_test.go: every read is the rule's own, answered from the
// read plan alone and loaded with IT05's LoadPartial, so a plan that reads what
// its read did not ask for panics here.

// R3.

func posResolveTwin() *posTwin {
	tw := newPosTwin("s1")
	tw.card("a", "s1", Waiting, 1)
	tw.card("b", "s1", Waiting, 2)
	tw.card("c", "s1", Waiting, 3)
	tw.sentinel("g", "s1", 5)
	tw.card("d", "s1", Waiting, 6)
	tw.card("e", "s1", Waiting, 7, "open", "1")
	return tw
}

func TestResolveReleasesBelowSigma(t *testing.T) {
	t.Parallel()
	tw := posResolveTwin()
	k := posKeyOf("resolve:s1")
	p := tw.plan(t, "resolve", 0, k)
	posSameStrings(t, "released", posUnitIDs(p), []string{"a", "b", "c"})
	for _, u := range p.Plan.Units {
		e := u.Changes[0].Entry
		if e.Move == nil || e.Move.Col != Ready || e.Move.Row != "s1" || e.Expect == nil || e.Expect.Place == nil ||
			e.Expect.Place.Col != Waiting || e.Expect.Revision != "" {
			t.Fatalf("%s is not a place-only move waiting -> ready: %+v", u.Key, e)
		}
	}
	gs := posGuardsOf(t, p)
	if len(gs) != 1 || gs[0].Kind != GuardZGuard || gs[0].Key != "sent:s1" || gs[0].Min != "-inf" || gs[0].Max != "3" ||
		gs[0].AtMost == nil || *gs[0].AtMost != 0 || gs[0].AtLeast != nil {
		t.Fatalf("the release guard: %+v", gs)
	}
	if len(p.Notes) != 1 || p.Notes[0].Op != posKnow || p.Notes[0].Type != posNoticeMadeReady || !strings.HasPrefix(p.Notes[0].Text, "3 cards of s1") {
		t.Fatalf("the notice: %+v", p.Notes)
	}
	if !posHas(p.Done, k.Key) || len(p.Requeue) != 0 || len(p.HeldBack) != 0 {
		t.Fatalf("the key: done %v requeue %v heldback %v", p.Done, p.Requeue, p.HeldBack)
	}
	if out := tw.apply(p); out.refused != "" || !out.wrote {
		t.Fatalf("apply: %+v", out)
	}
	for _, id := range []string{"a", "b", "c"} {
		if tw.work[id].col != Ready {
			t.Fatalf("%s is %s", id, tw.work[id].col)
		}
	}
	// d is above σ and e counts a need: neither goes, and nothing more is
	// planned now that three cards are open before σ
	if tw.work["d"].col != Waiting || tw.work["e"].col != Waiting {
		t.Fatal("a card at or above σ, or counting a need, was released")
	}
	if q := tw.plan(t, "resolve", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestResolveNoSentinelReleasesAllEligible(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.card("a", "s1", Waiting, 1)
	tw.card("b", "s1", Waiting, 9.5)
	tw.card("c", "s1", Waiting, 4, "open", "2")
	p := tw.plan(t, "resolve", 0, posKeyOf("resolve:s1"))
	posSameStrings(t, "released", posUnitIDs(p), []string{"a", "b"})
	if gs := posGuardsOf(t, p); len(gs) != 1 || gs[0].Max != "9.5" {
		t.Fatalf("the guard names the highest score released: %+v", gs)
	}
}

func TestResolveCutRequeuesForProgress(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	for i := 1; i <= 5; i++ {
		tw.card("c"+strconv.Itoa(i), "s1", Waiting, float64(i))
	}
	k := posKeyOf("resolve:s1")
	// eleven halvings plan the read at a chunk of one
	for want := 1; want <= 5; want++ {
		p, out := tw.run(t, "resolve", posMaxHalvings, k)
		if out.refused != "" || len(p.Plan.Units) != 1 {
			t.Fatalf("run %d: %d moves, %+v", want, len(p.Plan.Units), out)
		}
		last := want == 5
		if last && (!posHas(p.Done, k.Key) || len(p.Requeue) != 0) {
			t.Fatalf("the last run: done %v requeue %v", p.Done, p.Requeue)
		}
		if !last && (!posHas(p.Requeue, k.Key) || len(p.Done) != 0) {
			t.Fatalf("run %d: done %v requeue %v", want, p.Done, p.Requeue)
		}
		if got := len(tw.elig("s1")); got != 5-want {
			t.Fatalf("after run %d elig holds %d", want, got)
		}
	}
}

// A head that names a card its definition excludes is refused, never moved
// (1.3.5: no planner silently passes over a head, and none moves what a rule's
// definition leaves out): the twin names the card in the head whatever its
// record says, as a store with a fault would.
func TestResolveRefusesACardTheDefinitionExcludes(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		place string
		kv    []string
		why   string
	}{
		"it counts a need":  {Waiting, []string{"open", "1"}, "counts 1 needs"},
		"it is refused":     {Waiting, []string{"refused", "resolve: a reason"}, "it is refused: resolve: a reason"},
		"it is not waiting": {Ready, nil, "not waiting in s1 (it is s1:ready)"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tw := newPosTwin("s1")
			tw.card("a", "s1", Waiting, 1)
			bad := tw.card("b", "s1", c.place, 2, c.kv...)
			tw.inject["s1"] = []string{bad.id}
			p := tw.plan(t, "resolve", 0, posKeyOf("resolve:s1"))
			posSameStrings(t, "released", posUnitIDs(p), []string{"a"})
			if len(p.Plan.Refused) != 1 || p.Plan.Refused[0].Key != "b" || !strings.Contains(p.Plan.Refused[0].Why, c.why) {
				t.Fatalf("refusals: %+v, want b: %s", p.Plan.Refused, c.why)
			}
			if out := tw.apply(p); out.refused != "" || tw.work["b"].col != c.place {
				t.Fatalf("apply: %+v, b is %s", out, tw.work["b"].col)
			}
		})
	}
}

func TestReachGuardRcountZero(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.card("done1", "s1", Landed, 1)
	tw.sentinel("g", "s1", 5)
	tw.card("late", "s1", Waiting, 9)
	k := posKeyOf("resolve:s1")
	p := tw.plan(t, "resolve", 0, k)
	if len(p.Plan.Units) != 1 || p.Plan.Units[0].Key != "g" {
		t.Fatalf("units: %v", posUnitIDs(p))
	}
	e := p.Plan.Units[0].Changes[0].Entry
	if e.Move != nil || e.Expect == nil || e.Expect.Revision != "1" || e.Expect.Place.Col != Waiting {
		t.Fatalf("G is guarded at waiting with its revision: %+v", e)
	}
	gs := posGuardsOf(t, p)
	if len(gs) != 1 || gs[0].Kind != GuardRCount || gs[0].Table != Work || gs[0].Min != "-inf" || gs[0].Max != "(5" ||
		gs[0].AtMost == nil || *gs[0].AtMost != 0 || gs[0].AtLeast != nil {
		t.Fatalf("the reach guard: %+v", gs)
	}
	posSameStrings(t, "cells", gs[0].Cells, []string{"s1:waiting", "s1:ready", "s1:working", "s1:review", "s1:merging"})
	if len(p.Notes) != 1 || p.Notes[0].Op != posOpen || p.Notes[0].Type != NSentinelReached || !reflect.DeepEqual(p.Notes[0].Subjects, []string{"g"}) {
		t.Fatalf("the judgment: %+v", p.Notes)
	}
	if out := tw.apply(p); out.refused != "" || !tw.opened(NSentinelReached, "", "g") {
		t.Fatalf("apply %+v, judged %v", out, tw.judged)
	}
	if q := tw.plan(t, "resolve", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestReachNotRaised(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*posTwin){
		"a card is open before σ": func(tw *posTwin) { tw.card("w", "s1", Working, 2) },
		"G counts a need":         func(tw *posTwin) { tw.work["g"].f["open"] = "1" },
		"G is quarantined":        func(tw *posTwin) { tw.quar["g"] = true },
		"the judgment is open":    func(tw *posTwin) { tw.judge(NSentinelReached, "", "g") },
		"the judgment is held":    func(tw *posTwin) { tw.hold(NSentinelReached, "", "g") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tw := newPosTwin("s1")
			tw.card("done1", "s1", Landed, 1)
			tw.sentinel("g", "s1", 5)
			mutate(tw)
			p := tw.plan(t, "resolve", 0, posKeyOf("resolve:s1"))
			for _, n := range p.Notes {
				if n.Op == posOpen {
					t.Fatalf("raised: %+v", n)
				}
			}
		})
	}
}

// A judgment the coordinator holds is still on the sentinel (jopen:G keeps the
// hold beside the open ones): it is not raised again, and it closes when a card
// comes before the sentinel.
func TestHeldReachJudgmentClosesOnUnreach(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.sentinel("g", "s1", 5)
	tw.hold(NSentinelReached, "", "g")
	k := posKeyOf("resolve:s1")
	if p := tw.plan(t, "resolve", 0, k); len(p.Notes) != 0 || len(p.Guards) != 0 || len(p.Plan.Units) != 0 {
		t.Fatalf("a held judgment was raised again: %+v", p)
	}
	tw.card("in", "s1", Ready, 3)
	p, out := tw.run(t, "resolve", 0, k)
	if out.refused != "" || len(p.Notes) != 1 || p.Notes[0].Op != posClose || p.Notes[0].Type != NSentinelReached {
		t.Fatalf("a card came before it: %+v %+v", p.Notes, out)
	}
	if len(tw.held) != 0 || tw.opened(NSentinelReached, "", "g") {
		t.Fatalf("the held judgment stays: held %v open %v", tw.held, tw.judged)
	}
	if q := tw.plan(t, "resolve", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

// The reach guard is what keeps a card from being before a reached sentinel:
// a card that comes before σ between the read and the apply refuses the step,
// and the same plan without its guard applies and breaks PositionHolds (the
// reversed witness of the guard).
func TestReachRefusedWhenACardComesBeforeSigma(t *testing.T) {
	t.Parallel()
	build := func() (*posTwin, RulePlan) {
		tw := newPosTwin("s1")
		tw.card("done1", "s1", Landed, 1)
		tw.sentinel("g", "s1", 5)
		p := tw.plan(t, "resolve", 0, posKeyOf("resolve:s1"))
		tw.card("in", "s1", Waiting, 3) // an insertion in line, after the read
		return tw, p
	}
	tw, p := build()
	if out := tw.apply(p); out.refused == "" || tw.opened(NSentinelReached, "", "g") {
		t.Fatalf("the plan was not refused: %+v, judged %v", out, tw.judged)
	}
	tw, p = build()
	p.Guards = nil // the witness: no guard
	if out := tw.apply(p); out.refused != "" {
		t.Fatalf("without its guard the plan should apply: %+v", out)
	}
	if why := tw.posPositionHolds("s1"); why == "" {
		t.Fatal("without the guard PositionHolds should be broken, and it is not")
	}
}

func TestUnreachAtLeastOne(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.sentinel("g", "s1", 5)
	tw.card("in", "s1", Ready, 3)
	tw.judge(NSentinelReached, "", "g")
	k := posKeyOf("resolve:s1")
	p := tw.plan(t, "resolve", 0, k)
	if len(p.Plan.Units) != 0 || len(p.Notes) != 1 || p.Notes[0].Op != posClose || p.Notes[0].Type != NSentinelReached ||
		!strings.Contains(p.Notes[0].Text, "1 cards now before it") {
		t.Fatalf("plan: units %v notes %+v", posUnitIDs(p), p.Notes)
	}
	gs := posGuardsOf(t, p)
	if len(gs) != 1 || gs[0].AtLeast == nil || *gs[0].AtLeast != 1 || gs[0].AtMost != nil || gs[0].Max != "(5" {
		t.Fatalf("the unreach guard: %+v", gs)
	}
	// the card lands between read and apply: the guard refuses, and the
	// judgment stays
	tw.work["in"].col = Landed
	if out := tw.apply(p); out.refused == "" || !tw.opened(NSentinelReached, "", "g") {
		t.Fatalf("apply after the card landed: %+v", out)
	}
	tw.work["in"].col = Ready
	if out := tw.apply(p); out.refused != "" || tw.opened(NSentinelReached, "", "g") {
		t.Fatalf("apply: %+v", out)
	}
	if q := tw.plan(t, "resolve", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

// A sentinel placed at or before a released card between the read and the
// apply refuses the release; without its guard the release puts a card ready
// behind a sentinel (W4, in unit form).
func TestReleaseRefusedWhenASentinelIsInsertedMeanwhile(t *testing.T) {
	t.Parallel()
	build := func() (*posTwin, RulePlan) {
		tw := newPosTwin("s1")
		tw.card("a", "s1", Waiting, 1)
		tw.card("b", "s1", Waiting, 3)
		p := tw.plan(t, "resolve", 0, posKeyOf("resolve:s1"))
		tw.sentinel("late", "s1", 2)
		return tw, p
	}
	tw, p := build()
	if out := tw.apply(p); out.refused == "" || tw.work["b"].col != Waiting {
		t.Fatalf("the release was not refused: %+v, b is %s", out, tw.work["b"].col)
	}
	tw, p = build()
	p.Guards = nil
	if out := tw.apply(p); out.refused != "" {
		t.Fatalf("without its guard the release should apply: %+v", out)
	}
	if why := tw.posPositionHolds("s1"); why == "" {
		t.Fatal("without the guard PositionHolds should be broken, and it is not")
	}
}

func TestResolveHeldBackForADroppingStream(t *testing.T) {
	t.Parallel()
	tw := posResolveTwin()
	tw.dropping["s1"] = true
	k := posKeyOf("resolve:s1")
	p := tw.plan(t, "resolve", 0, k)
	if len(p.Plan.Units) != 0 || len(p.Guards) != 0 || len(p.Done) != 0 || len(p.Requeue) != 0 || !posHas(p.HeldBack, k.Key) {
		t.Fatalf("a dropping stream: %+v", p)
	}
}

// R4.

func TestNeedsTwoLandInOneTick(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.card("w", "s1", Waiting, 10, "open", "2", "needs", "a,b")
	tw.card("a", "s2", Landed, 1)
	tw.card("b", "s2", Landed, 2)
	tw.waiter("a", "w")
	tw.waiter("b", "w")
	tw.lines[7] = []string{"a", "b"}
	k := AgendaKey{Key: "needs@7", Seq: 7}
	p := tw.plan(t, "needs", 0, k)
	if len(p.Intents) != 2 || !reflect.DeepEqual(p.Intents[0], Intent{Kind: posNeedmet, Need: "a", Waiters: []string{"w"}}) ||
		!reflect.DeepEqual(p.Intents[1], Intent{Kind: posNeedmet, Need: "b", Waiters: []string{"w"}}) {
		t.Fatalf("intents: %+v", p.Intents)
	}
	if len(p.Plan.Units) != 0 || len(p.Guards) != 0 {
		t.Fatalf("the needs rule guards no waiter and writes no entry: units %d guards %d", len(p.Plan.Units), len(p.Guards))
	}
	if !posHas(p.Done, "needs@7") || len(p.Requeue) != 0 {
		t.Fatalf("the key: %v %v", p.Done, p.Requeue)
	}
	if out := tw.apply(p); out.refused != "" || tw.work["w"].f["open"] != "0" {
		t.Fatalf("two needs landing in one tick lower open by two: open=%s, %+v", tw.work["w"].f["open"], out)
	}
	if got := tw.elig("s1"); len(got) != 1 || got[0].id != "w" {
		t.Fatal("w did not enter elig by derivation")
	}
	if q := tw.plan(t, "needs", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestNeedsGoneHeadMoves(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	for i := 1; i <= 3; i++ {
		id := "w" + strconv.Itoa(i)
		tw.card(id, "s1", Waiting, float64(i), "open", "1", "needs", "n")
	}
	tw.work["n"] = &posRec{id: "n", rev: 1, f: map[string]string{"outcome": "dropped"}} // kept, off the table
	tw.waiter("n", "w1", "w2", "w3")
	k := posKeyOf("needs:n")
	// a chunk of one: each run serves the head, which strictly moves
	for run := 1; run <= 3; run++ {
		before := len(tw.wait["n"])
		p, out := tw.run(t, "needs", posMaxHalvings, k)
		if out.refused != "" || len(p.Intents) != 1 || p.Intents[0].Kind != posNeedgone || len(p.Intents[0].Waiters) != 1 {
			t.Fatalf("run %d: %+v %+v", run, p.Intents, out)
		}
		if got := len(tw.wait["n"]); got != before-1 {
			t.Fatalf("run %d: wait:n went from %d to %d", run, before, got)
		}
		if last := run == 3; last == posHas(p.Requeue, k.Key) || last != posHas(p.Done, k.Key) {
			t.Fatalf("run %d: done %v requeue %v", run, p.Done, p.Requeue)
		}
	}
	for _, id := range []string{"w1", "w2", "w3"} {
		if tw.work[id].f["open"] != "1" || !tw.opened(NBlocked, "n", id) {
			t.Fatalf("%s: open %s, judged %v", id, tw.work[id].f["open"], tw.judged)
		}
	}
	if q := tw.plan(t, "needs", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestNeedsKeepsKeyForDroppingWaiter(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.card("w1", "s1", Waiting, 1, "open", "1", "needs", "n")
	tw.card("w2", "s2", Waiting, 2, "open", "1", "needs", "n")
	tw.card("n", "s1", Landed, 0)
	tw.waiter("n", "w1", "w2")
	tw.dropping["s2"] = true
	k := posKeyOf("needs:n")
	// the waiter of the stream being dropped is left out, and the key stays for
	// it: requeued while the other waiter makes progress, held back after
	p, out := tw.run(t, "needs", 0, k)
	if out.refused != "" || len(p.Intents) != 1 || !reflect.DeepEqual(p.Intents[0].Waiters, []string{"w1"}) {
		t.Fatalf("first run: %+v %+v", p.Intents, out)
	}
	if !posHas(p.Requeue, k.Key) || len(p.Done) != 0 || len(p.HeldBack) != 0 {
		t.Fatalf("first run key: done %v requeue %v heldback %v", p.Done, p.Requeue, p.HeldBack)
	}
	p, _ = tw.run(t, "needs", 0, k)
	if len(p.Intents) != 0 || !posHas(p.HeldBack, k.Key) || len(p.Done) != 0 || len(p.Requeue) != 0 {
		t.Fatalf("second run: intents %+v done %v requeue %v heldback %v", p.Intents, p.Done, p.Requeue, p.HeldBack)
	}
	if tw.work["w2"].f["open"] != "1" {
		t.Fatal("a waiter of a dropping stream was served")
	}
	delete(tw.dropping, "s2")
	p, _ = tw.run(t, "needs", 0, k)
	if len(p.Intents) != 1 || !posHas(p.Done, k.Key) || tw.work["w2"].f["open"] != "0" {
		t.Fatalf("after the mark cleared: %+v done %v open %s", p.Intents, p.Done, tw.work["w2"].f["open"])
	}
}

func TestNeedsOpenNeedPlansNothing(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.card("w", "s1", Waiting, 1, "open", "1", "needs", "n")
	tw.card("n", "s1", Working, 0)
	tw.waiter("n", "w")
	p := tw.plan(t, "needs", 0, posKeyOf("needs:n"))
	if len(p.Intents) != 0 || len(p.Notes) != 0 || len(p.Done) != 1 {
		t.Fatalf("an open need: %+v", p)
	}
}

func TestMadeClosesMissingKeepsWaiting(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.card("w", "s1", Waiting, 5, "open", "1", "needs", "n")
	tw.waiter("n", "w")
	tw.missing["n"] = true
	tw.judge(NMissingNeed, "n", "w")
	// n is created by line 9, with another card that no one waits for
	tw.card("n", "s2", Waiting, 1)
	tw.card("other", "s2", Waiting, 2)
	tw.lines[9] = []string{"other", "n"}
	k := AgendaKey{Key: "made@9", Seq: 9}
	p := tw.plan(t, "needs", 0, k)
	if len(p.Notes) != 1 || p.Notes[0].Op != posClose || p.Notes[0].Type != NMissingNeed || p.Notes[0].Cause != "n" ||
		!reflect.DeepEqual(p.Notes[0].Subjects, []string{"w"}) {
		t.Fatalf("notes: %+v", p.Notes)
	}
	if len(p.Intents) != 1 || !reflect.DeepEqual(p.Intents[0], Intent{Kind: posMade, Need: "n", Waiters: []string{"w"}}) {
		t.Fatalf("intents: %+v", p.Intents)
	}
	if !posHas(p.Done, "made@9") {
		t.Fatalf("done: %v", p.Done)
	}
	if out := tw.apply(p); out.refused != "" {
		t.Fatalf("apply: %+v", out)
	}
	if tw.missing["n"] || tw.opened(NMissingNeed, "n", "w") {
		t.Fatalf("missing %v, judged %v", tw.missing, tw.judged)
	}
	// the waiter stays in wait:n with open unchanged: it now waits for n to land
	if !tw.wait["n"]["w"] || tw.work["w"].f["open"] != "1" || tw.work["w"].col != Waiting {
		t.Fatal("the waiter did not stay waiting")
	}
	if q := tw.plan(t, "needs", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestMadeCarriedKeyReadsLine(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = "m" + strconv.Itoa(i)
		tw.card(ids[i], "s2", Waiting, float64(i))
	}
	tw.lines[9] = ids
	tw.card("w", "s1", Waiting, 500, "open", "1", "needs", "m230")
	tw.waiter("m230", "w")
	tw.missing["m230"] = true
	tw.judge(NMissingNeed, "m230", "w")
	// the key first queued for the line is read as the line and its first
	// window of ids, not as a list of ids
	first := AgendaKey{Key: "made@9", Seq: 9}
	rp, left := posRule(t, "needs").Read([]AgendaKey{first}, posBounds, 0)
	if src := rp.Sprint[0].Source; len(left) != 0 || len(rp.Sprint) != 1 || src.Kind != SourceLine || src.Seq != 9 || src.Offset != 0 ||
		src.Limit != needsLineWindow || len(src.IDs) != 0 || !rp.Sprint[0].Missing || len(rp.IDs) != 0 {
		t.Fatalf("read plan: %+v left %v", rp, left)
	}
	// nothing in the first window is missing: the key moves on to the next
	// window with the order it had
	p, out := tw.run(t, "needs", 0, first)
	if out.refused != "" || len(p.Notes) != 0 || len(p.Intents) != 0 || !posHas(p.Done, "made@9") ||
		len(p.Requeue) != 1 || p.Requeue[0] != (AgendaKey{Key: "made@9+100", Seq: 9}) {
		t.Fatalf("first window: done %v requeue %v notes %+v", p.Done, p.Requeue, p.Notes)
	}
	next := p.Requeue[0]
	rp, _ = posRule(t, "needs").Read([]AgendaKey{next}, posBounds, 0)
	if src := rp.Sprint[0].Source; src.Kind != SourceLine || src.Seq != 9 || src.Offset != 100 || src.Limit != needsLineWindow {
		t.Fatalf("the carried key reads %+v", rp.Sprint[0])
	}
	p, _ = tw.run(t, "needs", 0, next)
	posSameStrings(t, "second window requeue", keyTexts(p.Requeue), []string{"made@9+200"})
	// the hit is in the last window, which ends the line
	p, out = tw.run(t, "needs", 0, AgendaKey{Key: "made@9+200", Seq: 9})
	if out.refused != "" || len(p.Intents) != 1 || p.Intents[0].Need != "m230" || !posHas(p.Done, "made@9+200") || len(p.Requeue) != 0 {
		t.Fatalf("last window: %+v done %v requeue %v", p.Intents, p.Done, p.Requeue)
	}
	if tw.missing["m230"] || tw.opened(NMissingNeed, "m230", "w") {
		t.Fatal("the missing need was not closed")
	}
}

func keyTexts(keys []AgendaKey) []string {
	var out []string
	for _, k := range keys {
		out = append(out, k.Key)
	}
	return out
}

func TestMadeKeepsMissingWhileAWaiterIsFrozen(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.card("w1", "s1", Waiting, 1, "open", "1", "needs", "n")
	tw.card("w2", "s2", Waiting, 2, "open", "1", "needs", "n")
	tw.waiter("n", "w1", "w2")
	tw.missing["n"] = true
	tw.judge(NMissingNeed, "n", "w1")
	tw.judge(NMissingNeed, "n", "w2")
	tw.card("n", "s1", Waiting, 0)
	tw.dropping["s2"] = true
	k := posKeyOf("made:n")
	// the need is left out whole while one of its waiters is frozen: no close, no
	// removal from missing, and the key stays, held back
	for run := 1; run <= 2; run++ {
		p, out := tw.run(t, "needs", 0, k)
		if out.refused != "" || out.wrote || len(p.Notes) != 0 || len(p.Intents) != 0 || !posHas(p.HeldBack, k.Key) ||
			len(p.Done) != 0 || len(p.Requeue) != 0 {
			t.Fatalf("run %d: %+v notes %+v intents %+v heldback %v", run, out, p.Notes, p.Intents, p.HeldBack)
		}
	}
	if !tw.missing["n"] || !tw.opened(NMissingNeed, "n", "w1") || !tw.opened(NMissingNeed, "n", "w2") {
		t.Fatal("a need with a frozen waiter was partly closed")
	}
	delete(tw.dropping, "s2")
	p, _ := tw.run(t, "needs", 0, k)
	if len(p.Intents) != 1 || len(p.Notes) != 1 || !reflect.DeepEqual(p.Notes[0].Subjects, []string{"w1", "w2"}) ||
		tw.missing["n"] || tw.opened(NMissingNeed, "n", "w1") || tw.opened(NMissingNeed, "n", "w2") || !posHas(p.Done, k.Key) {
		t.Fatalf("after the mark cleared: %+v missing %v judged %v", p.Intents, tw.missing, tw.judged)
	}
}

// R5.

func posCrossTwin() *posTwin {
	tw := newPosTwin("s1", "s2", "s3")
	tw.ctl("s1", "state", StreamStopped, "cause", "cross", "other", "x", "card", "m1")
	tw.stuck("s1", "m1", 1)
	tw.stuck("s1", "m2", 2)
	tw.merge["q1"] = &posRec{id: "q1", row: "s1", col: Queued, score: 3, rev: 1, f: map[string]string{}}
	tw.card("x", "s2", Landed, 1)
	tw.ctl("s2")
	tw.ctl("s3", "state", StreamStopped, "cause", "cross", "other", "y", "card", "z1")
	tw.stuck("s3", "z1", 1)
	tw.card("y", "s2", Working, 2) // its need has not landed
	return tw
}

func TestCrossResumes(t *testing.T) {
	t.Parallel()
	tw := posCrossTwin()
	k := posKeyOf("cross")
	p := tw.plan(t, "cross", 0, k)
	if len(p.Plan.Units) != 1 || p.Plan.Units[0].Stream != "s1" {
		t.Fatalf("units: %+v", p.Plan.Units)
	}
	changes := p.Plan.Units[0].Changes
	if len(changes) != 3 {
		t.Fatalf("changes: %d", len(changes))
	}
	ctl := changes[0].Entry
	if ctl.ID != "ctl-s1" || ctl.Expect == nil || ctl.Expect.Revision != "1" || ctl.Set["state"] != StreamMerging ||
		ctl.Set["since"] == "" || ctl.Set["due_mergeidle"] != strconv.FormatInt(tw.now.R+30*60*1000, 10) {
		t.Fatalf("the control card change: %+v", ctl)
	}
	posSameStrings(t, "unset", ctl.Unset, []string{"cause", "card", "other"})
	for i, id := range []string{"m1", "m2"} {
		e := changes[i+1].Entry
		if changes[i+1].Table != Merge || e.ID != id || e.Move == nil || e.Move.Col != Queued || e.Move.Score != nil ||
			e.Expect == nil || e.Expect.Revision != "" || e.Expect.Place.Col != Stuck {
			t.Fatalf("stuck card %s: %+v", id, e)
		}
		// a resumed card is no longer waiting for a card of another stream
		posSameStrings(t, "unset of "+id, e.Unset, []string{"need_card", "need_stream"})
	}
	if len(p.Notes) != 2 || p.Notes[0].Op != posClose || p.Notes[0].Type != NCross || p.Notes[1].Op != posKnow || p.Notes[1].Type != NResumed {
		t.Fatalf("notes: %+v", p.Notes)
	}
	if !posHas(p.Done, "cross") {
		t.Fatalf("done: %v", p.Done)
	}
	tw.judge(NCross, "", StreamSubject("s1"))
	if out := tw.apply(p); out.refused != "" {
		t.Fatalf("apply: %+v", out)
	}
	if tw.merge["ctl-s1"].f["state"] != StreamMerging || tw.merge["m1"].col != Queued || tw.merge["m2"].col != Queued ||
		tw.merge["ctl-s3"].f["state"] != StreamStopped || tw.merge["z1"].col != Stuck {
		t.Fatal("the resume moved the wrong streams")
	}
	for _, id := range []string{"m1", "m2"} {
		if _, ok := tw.merge[id].f["need_card"]; ok || tw.merge[id].f["need_stream"] != "" {
			t.Fatalf("%s still names the card it needed: %v", id, tw.merge[id].f)
		}
	}
	if _, ok := tw.merge["z1"].f["need_card"]; !ok {
		t.Fatal("a stuck card of a stream that was not resumed lost what it needs")
	}
	if tw.opened(NCross, "", StreamSubject("s1")) {
		t.Fatal("the stop judgment stays open")
	}
	if q := tw.plan(t, "cross", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestCrossCutResumesAChunkAtATime(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.ctl("s1", "state", StreamStopped, "cause", "cross", "other", "x")
	tw.ctl("s2")
	tw.card("x", "s2", Landed, 1)
	for i := 1; i <= 3; i++ {
		tw.stuck("s1", "m"+strconv.Itoa(i), float64(i))
	}
	k := posKeyOf("cross")
	// eleven halvings read one stuck id a stream
	for run := 1; run <= 3; run++ {
		p, out := tw.run(t, "cross", posMaxHalvings, k)
		if out.refused != "" || len(p.Plan.Units) != 1 {
			t.Fatalf("run %d: %+v units %d", run, out, len(p.Plan.Units))
		}
		last := run == 3
		state := tw.merge["ctl-s1"].f["state"]
		if last != (state == StreamMerging) {
			t.Fatalf("run %d: the stream is %s", run, state)
		}
		if last != (len(p.Notes) == 2) {
			t.Fatalf("run %d: notes %+v", run, p.Notes)
		}
		if last != posHas(p.Done, "cross") || last == posHas(p.Requeue, "cross") {
			t.Fatalf("run %d: done %v requeue %v", run, p.Done, p.Requeue)
		}
	}
	for i := 1; i <= 3; i++ {
		if tw.merge["m"+strconv.Itoa(i)].col != Queued {
			t.Fatalf("m%d is %s", i, tw.merge["m"+strconv.Itoa(i)].col)
		}
	}
}

func TestCrossHeldBackForADroppingStream(t *testing.T) {
	t.Parallel()
	tw := posCrossTwin()
	tw.dropping["s1"] = true
	k := posKeyOf("cross")
	p := tw.plan(t, "cross", 0, k)
	if len(p.Plan.Units) != 0 || len(p.Done) != 0 || len(p.Requeue) != 0 || !posHas(p.HeldBack, "cross") {
		t.Fatalf("a dropping stream: %+v", p)
	}
}

// A stopped stream that is being dropped is left out while another stream
// resumes in the same plan, and the key stays in the agenda for it: the key is
// requeued (it moved something and left something out), held back on the next
// run, and planned again to resume the dropped stream's card once the mark
// clears. The unfreeze queues the rules it names (resolve, pullback, deal) and
// not cross, so a key that was marked done here would lose that resume.
func TestCrossKeepsItsKeyForADroppingStreamLeftOutBesideAResumedOne(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2", "s3")
	tw.card("x", "s3", Landed, 1)
	tw.ctl("s3")
	for _, s := range []string{"s1", "s2"} {
		tw.ctl(s, "state", StreamStopped, "cause", "cross", "other", "x", "card", "m-"+s)
		tw.stuck(s, "m-"+s, 1)
	}
	tw.dropping["s2"] = true
	k := posKeyOf("cross")
	tw.agenda[k.Key] = k.Seq
	p, out := tw.run(t, "cross", 0, k)
	if out.refused != "" || !reflect.DeepEqual(posUnitIDs(p), []string{"ctl-s1"}) || !posHas(p.Requeue, "cross") || posHas(p.Done, "cross") || len(p.HeldBack) != 0 {
		t.Fatalf("first run: %+v units %v requeue %v done %v held %v", out, posUnitIDs(p), p.Requeue, p.Done, p.HeldBack)
	}
	if _, ok := tw.agenda["cross"]; !ok || tw.merge["ctl-s2"].f["state"] != StreamStopped {
		t.Fatalf("the key is owed for s2: agenda %v, s2 is %s", tw.agenda, tw.merge["ctl-s2"].f["state"])
	}
	// nothing more moves while the mark stands: the key is held back
	p, out = tw.run(t, "cross", 0, k)
	if out.refused != "" || len(p.Plan.Units) != 0 || !posHas(p.HeldBack, "cross") || len(p.Done) != 0 || len(p.Requeue) != 0 {
		t.Fatalf("second run: %+v units %d held %v done %v requeue %v", out, len(p.Plan.Units), p.HeldBack, p.Done, p.Requeue)
	}
	delete(tw.dropping, "s2")
	tw.heldKeys = map[string]bool{}
	tw.drain(t, "cross", 0, 5)
	if tw.merge["ctl-s2"].f["state"] != StreamMerging || tw.merge["m-s2"].col != Queued || len(tw.agenda) != 0 {
		t.Fatalf("after the mark cleared: s2 is %s, m-s2 %s, agenda %v", tw.merge["ctl-s2"].f["state"], tw.merge["m-s2"].col, tw.agenda)
	}
}

func TestCrossStuckReadBounded(t *testing.T) {
	t.Parallel()
	rows := make([]string, 250)
	for i := range rows {
		rows[i] = "s" + strconv.Itoa(i)
	}
	tw := newPosTwin(rows...) // a sprint has at most MaxStreams streams: every one stopped, the needed card in the first
	tw.card("x", rows[0], Landed, 1)
	for _, r := range rows {
		tw.ctl(r, "state", StreamStopped, "cause", "cross", "other", "x")
		for j := 0; j < 81; j++ { // one more than the read's ids a stream
			tw.stuck(r, r+"-"+strconv.Itoa(j), float64(j))
		}
	}
	k := posKeyOf("cross")
	rp, left := posRule(t, "cross").Read([]AgendaKey{k}, posBounds, 0)
	if len(left) != 0 || len(rp.Sprint) != 1 || len(rp.Counts)+len(rp.RCounts)+len(rp.Ranges) != 0 {
		t.Fatalf("250 stopped streams are one read: %+v left %v", rp, left)
	}
	c := QueryCost(rp.Sprint[0])
	if c.Records > posBounds.Records || c.RangeIDs > posBounds.RangeIDs || c.Bytes > posBounds.Bytes || rp.Sprint[0].Limit != 80 {
		t.Fatalf("the read's declared cost %+v, stuck %d, is not within %+v", c, rp.Sprint[0].Limit, posBounds)
	}
	// what the read answers is within layer 1's range ids too: a read past a
	// bound is BUDGET and answers nothing
	answered := 0
	for _, v := range tw.answer(rp).Sprint[0].Stuck {
		answered += len(v.IDs)
	}
	if answered == 0 || answered > posBounds.RangeIDs {
		t.Fatalf("the read answered %d stuck ids, and layer 1 allows %d range ids", answered, posBounds.RangeIDs)
	}
	// each stream's first 80 stuck ids come back (no BUDGET), and are resumed
	p, out := tw.run(t, "cross", 0, k)
	if out.refused != "" || len(p.Plan.Units) != 250 || !posHas(p.Requeue, "cross") || len(p.Done) != 0 {
		t.Fatalf("plan: %d units, requeue %v, %+v", len(p.Plan.Units), p.Requeue, out)
	}
	for _, u := range p.Plan.Units {
		if len(u.Changes) != 1+80 {
			t.Fatalf("%s: %d changes", u.Key, len(u.Changes))
		}
	}
}

// R15.

func posDoneTwin(streams int, open bool) *posTwin {
	rows := make([]string, streams)
	for i := range rows {
		rows[i] = "s" + strconv.Itoa(i)
	}
	tw := newPosTwin(rows...)
	for _, r := range rows {
		tw.card(r+"-a", r, Landed, 1)
		tw.card(r+"-b", r, Landed, 2)
		tw.ctl(r, "dropped", "1")
	}
	if open {
		tw.card("late", rows[0], Waiting, 9)
	}
	return tw
}

func TestDoneOneRcountEntry(t *testing.T) {
	t.Parallel()
	tw := posDoneTwin(200, false)
	k := posKeyOf("done")
	p := tw.plan(t, "done", 0, k)
	gs := posGuardsOf(t, p)
	if len(gs) != 1 || gs[0].Kind != GuardRCount || len(gs[0].Cells) != 1000 || gs[0].Min != "-inf" || gs[0].Max != "+inf" ||
		gs[0].AtMost == nil || *gs[0].AtMost != 0 || gs[0].AtLeast != nil {
		t.Fatalf("200 streams give one rcount entry over 1,000 cells: %d guards", len(gs))
	}
	counters := 0
	for _, x := range p.Guards {
		if x.Kind == posCounter {
			counters++
			if x.Key != "streams" || x.Score != 200 {
				t.Fatalf("counter: %+v", x)
			}
		}
	}
	if counters != 1 || len(p.Guards) != 2 || len(p.Plan.Units) != 0 {
		t.Fatalf("guards %d counters %d units %d", len(p.Guards), counters, len(p.Plan.Units))
	}
	if len(p.Notes) != 1 || p.Notes[0].Op != posOpen || p.Notes[0].Type != NSprintDone || p.Notes[0].Text != "400 landed, 200 dropped" ||
		!reflect.DeepEqual(p.Notes[0].Subjects, []string{SprintSubject}) {
		t.Fatalf("the judgment: %+v", p.Notes)
	}
	if !posHas(p.Done, "done") {
		t.Fatalf("done: %v", p.Done)
	}
	if out := tw.apply(p); out.refused != "" || !tw.opened(NSprintDone, "", SprintSubject) {
		t.Fatalf("apply: %+v", out)
	}
	if q := tw.plan(t, "done", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestDoneStreamsCounterGuard(t *testing.T) {
	t.Parallel()
	tw := posDoneTwin(3, false)
	k := posKeyOf("done")
	p := tw.plan(t, "done", 0, k)
	// a stream is added between the read and the apply: COUNTER, no judgment
	tw.rows = append(tw.rows, "s-new")
	tw.streams++
	tw.card("fresh", "s-new", Waiting, 1)
	out := tw.apply(p)
	if out.refused != "COUNTER" || tw.opened(NSprintDone, "", SprintSubject) {
		t.Fatalf("apply after a stream was added: %+v, judged %v", out, tw.judged)
	}
	// the next plan sees the new stream and its open card: nothing is done
	p = tw.plan(t, "done", 0, k)
	if len(p.Notes) != 0 {
		t.Fatalf("the sprint is not done: %+v", p.Notes)
	}
	// the close carries the counter too (2.3: COUNTER on {p}next@e.streams as
	// read): a stream added between the read and the apply refuses it, and the
	// judgment stays open
	tw = posDoneTwin(3, true)
	tw.judge(NSprintDone, "", SprintSubject)
	p = tw.plan(t, "done", 0, k)
	if len(p.Notes) != 1 || p.Notes[0].Op != posClose {
		t.Fatalf("the close: %+v", p.Notes)
	}
	tw.rows = append(tw.rows, "s-new")
	tw.streams++
	tw.card("fresh", "s-new", Waiting, 1)
	out = tw.apply(p)
	if out.refused != "COUNTER" || !tw.opened(NSprintDone, "", SprintSubject) {
		t.Fatalf("apply of a close after a stream was added: %+v, judged %v", out, tw.judged)
	}
}

func TestDoneClosesWhenWorkAdded(t *testing.T) {
	t.Parallel()
	tw := posDoneTwin(3, true)
	tw.judge(NSprintDone, "", SprintSubject)
	k := posKeyOf("done")
	p := tw.plan(t, "done", 0, k)
	gs := posGuardsOf(t, p)
	if len(p.Notes) != 1 || p.Notes[0].Op != posClose || p.Notes[0].Type != NSprintDone || p.Notes[0].Text != "work was added" ||
		len(gs) != 1 || gs[0].AtLeast == nil || *gs[0].AtLeast != 1 || gs[0].AtMost != nil {
		t.Fatalf("plan: notes %+v guards %+v", p.Notes, gs)
	}
	if out := tw.apply(p); out.refused != "" || tw.opened(NSprintDone, "", SprintSubject) {
		t.Fatalf("apply: %+v", out)
	}
	if q := tw.plan(t, "done", 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

func TestDoneNotRaisedForASprintWithNoCard(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.ctl("s1")
	p := tw.plan(t, "done", 0, posKeyOf("done"))
	if len(p.Notes) != 0 || len(p.Guards) != 0 || !posHas(p.Done, "done") {
		t.Fatalf("an empty sprint is not done: %+v", p)
	}
}

func TestDoneOpenCardsNoJudgmentPlansNothing(t *testing.T) {
	t.Parallel()
	tw := posDoneTwin(2, true)
	p := tw.plan(t, "done", 0, posKeyOf("done"))
	if !posQuiet(RulePlan{Plan: p.Plan, Intents: p.Intents, Guards: p.Guards, Notes: p.Notes}) || len(p.Done) != 1 {
		t.Fatalf("open cards and no judgment: %+v", p)
	}
}

// R19.

func TestPullBackAfterDealOneStep(t *testing.T) {
	t.Parallel()
	table := RuleTable()
	var pull Rule
	position := map[string]int{}
	for i, r := range table {
		position[r.Name] = i
		if r.Name == posPullbackRule {
			pull = r
		}
	}
	if pull.Name == "" || pull.MaxSteps != 1 {
		t.Fatalf("pullback is not registered with one step a tick: %+v", pull)
	}
	// the design's order (1.4.2): pull back after every other rule, and after
	// deal, whose place is R6's between cross and level
	for _, name := range []string{ruleNeeds, ruleResolve, ruleCross, ruleDone} {
		if position[name] >= position[posPullbackRule] {
			t.Fatalf("%s sorts after the pull back", name)
		}
	}
	deal, _ := PriorityOf("deal")
	done, _ := PriorityOf(ruleDone)
	if deal == 0 || pull.Priority <= deal || pull.Priority <= done {
		t.Fatalf("the pull back (%d) is not after deal (%d) and done (%d)", pull.Priority, deal, done)
	}
	// one stream with more cards above σ than a step holds: one chunk, and the
	// key stays
	tw := newPosTwin("s1")
	tw.sentinel("g", "s1", 1)
	for i := 0; i < 5000; i++ {
		tw.card("r"+strconv.Itoa(i), "s1", Ready, float64(2+i))
	}
	k := posKeyOf("pullback:s1")
	p := tw.plan(t, posPullbackRule, 0, k)
	moves := 0
	for _, u := range p.Plan.Units {
		if e := u.Changes[0].Entry; e.Move != nil {
			moves++
		}
	}
	if moves != positionChunk || len(p.Plan.Units) != positionChunk+1 || !posHas(p.Requeue, "pullback:s1") || len(p.Done) != 0 {
		t.Fatalf("moves %d units %d requeue %v done %v", moves, len(p.Plan.Units), p.Requeue, p.Done)
	}
	if lawful := Lawful(p.Plan); len(lawful.Refused) != 0 || len(lawful.Units) != len(p.Plan.Units) {
		t.Fatalf("the lifecycle refuses the pull back: %+v", lawful.Refused)
	}
	first := p.Plan.Units[0]
	if e := first.Changes[0].Entry; e.ID != "g" || e.Move != nil || e.Expect.Place.Col != Waiting || e.Expect.Revision != "" {
		t.Fatalf("G is guarded at waiting: %+v", e)
	}
	moved := p.Plan.Units[1]
	if e := moved.Changes[0].Entry; e.Move == nil || e.Move.Col != Waiting || e.Expect.Place.Col != Ready || e.Expect.Revision != "" ||
		!strings.Contains(moved.Moved, "behind sentinel g") {
		t.Fatalf("a pulled card: %+v %s", e, moved.Moved)
	}
	if out := tw.apply(p); out.refused != "" {
		t.Fatalf("apply: %+v", out)
	}
	if got := tw.count("s1", Waiting); got != positionChunk+1 || len(tw.fresh("s1")) != 3000 {
		t.Fatalf("waiting %d, fresh %d", got, len(tw.fresh("s1")))
	}
}

func TestPullBackStreamsShareOneStep(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2", "s3")
	for _, s := range tw.rows {
		tw.sentinel(s+"-g", s, 1)
		for i := 0; i < 1500; i++ {
			tw.card(s+"-r"+strconv.Itoa(i), s, Ready, float64(2+i))
		}
	}
	keys := []AgendaKey{posKeyOf("pullback:s1"), posKeyOf("pullback:s2"), posKeyOf("pullback:s3")}
	p := tw.plan(t, posPullbackRule, 0, keys...)
	moves := 0
	for _, u := range p.Plan.Units {
		if u.Changes[0].Entry.Move != nil {
			moves++
		}
	}
	if moves > positionChunk || moves < positionChunk-len(keys) || len(p.Requeue) != 3 {
		t.Fatalf("three streams share one chunk: %d moves, requeue %v", moves, p.Requeue)
	}
}

func TestPullBackNothingToPull(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2", "s3")
	tw.card("a", "s1", Ready, 1) // no sentinel
	tw.sentinel("g2", "s2", 1)   // nothing ready above it
	tw.sentinel("g3", "s3", 1)
	tw.card("b", "s3", Ready, 5)
	tw.quar["g3"] = true
	keys := []AgendaKey{posKeyOf("pullback:s1"), posKeyOf("pullback:s2"), posKeyOf("pullback:s3")}
	p := tw.plan(t, posPullbackRule, 0, keys...)
	if len(p.Plan.Units) != 0 || len(p.Notes) != 0 || len(p.Done) != 3 {
		t.Fatalf("no sentinel, none above it, a quarantined sentinel: units %d notes %d done %v", len(p.Plan.Units), len(p.Notes), p.Done)
	}
}

func TestPullBackHeldBackForADroppingStream(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.sentinel("g", "s1", 1)
	tw.card("r", "s1", Ready, 2)
	tw.dropping["s1"] = true
	p := tw.plan(t, posPullbackRule, 0, posKeyOf("pullback:s1"))
	if len(p.Plan.Units) != 0 || len(p.Done) != 0 || !posHas(p.HeldBack, "pullback:s1") {
		t.Fatalf("a dropping stream: %+v", p)
	}
}

// The registry, the reads and the keys.

func TestPositionRulesRegistered(t *testing.T) {
	t.Parallel()
	want := []struct {
		name  string
		steps int
	}{{ruleNeeds, 0}, {ruleResolve, 0}, {ruleCross, 0}, {ruleDone, 0}, {posPullbackRule, 1}}
	byName := map[string]Rule{}
	for _, r := range RuleTable() {
		byName[r.Name] = r
	}
	for _, w := range want {
		// the priority is IT05's row for the rule, by name
		p, ok := PriorityOf(w.name)
		r, found := byName[w.name]
		if !ok || !found || r.Priority != p || r.MaxSteps != w.steps || r.Read == nil || r.Plan == nil {
			t.Fatalf("%s: %+v (row %d, %v)", w.name, r, p, ok)
		}
	}
	prev := -1
	for _, r := range RuleTable() {
		if r.Priority < prev {
			t.Fatalf("the table is not in priority order at %s", r.Name)
		}
		prev = r.Priority
	}
}

// Every key a rule of this file reads or puts in the agenda is served by a
// registered rule (2.1): ServingRule (IT05) sends the keys of made to R4 and
// leaves the others to the rule of their word, and each of these rules is
// registered, with a priority.
func TestEveryKeyOfThePositionRulesIsServed(t *testing.T) {
	t.Parallel()
	registered := map[string]Rule{}
	for _, r := range RuleTable() {
		registered[r.Name] = r
	}
	for rule, words := range positionKeyWords {
		if _, ok := registered[rule]; !ok {
			t.Errorf("%s is not registered", rule)
		}
		for _, word := range words {
			for _, key := range []string{word, word + ":x", word + "@7", word + "@7+100"} {
				if got := ServingRule(key); got != rule {
					t.Errorf("ServingRule(%q) = %q, want %s", key, got, rule)
				}
			}
		}
	}
	for _, key := range []string{"made:n", "made:n+w3", "made@9", "made@9+100", "needs:n", "needs@9+100", "resolve:s1", "cross", "done", "pullback:s1"} {
		rule := ServingRule(key)
		if _, ok := registered[rule]; !ok {
			t.Errorf("the key %q is served by %q, which is not registered", key, rule)
		}
		if p, ok := PriorityOf(rule); !ok || p != registered[rule].Priority {
			t.Errorf("the key %q: rule %q has priority %d, %v", key, rule, p, ok)
		}
		if p, _ := posSplitKey(AgendaKey{Key: key}); !posServes(rule, p.rule) {
			t.Errorf("the key %q is served by %q, which does not read its word %q", key, rule, p.rule)
		}
	}
}

// A plan on a read that did not load what the plan reads is refused: in a test
// build it panics at the read (IT05), and in a release build it plans nothing,
// keeps every key, and the snapshot names what it read (UnloadedErr), so that the
// tick refuses the plan.
func TestNoAnswerKeepsTheKey(t *testing.T) {
	t.Parallel()
	tw := posResolveTwin()
	for name, key := range map[string]string{ruleResolve: "resolve:s1", ruleNeeds: "needs:n", ruleCross: "cross", ruleDone: "done", posPullbackRule: "pullback:s1"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			keys := []AgendaKey{posKeyOf(key)}
			// a read that asked for nothing
			s, err := loadPartial(ReadPlan{}, tw.answer(ReadPlan{}), false)
			if err != nil {
				t.Fatal(err)
			}
			p := posRule(t, name).Plan(s, keys, tw.now)
			if len(p.Done)+len(p.Requeue)+len(p.HeldBack)+len(p.Plan.Units)+len(p.Notes)+len(p.Intents) != 0 {
				t.Fatalf("%s planned on no answer: %+v", name, p)
			}
			if !errors.Is(s.UnloadedErr(), ErrUnloaded) {
				t.Fatalf("%s read what no read loaded, and the snapshot says nothing: %v", name, s.Unloaded())
			}
			strict, err := LoadPartial(ReadPlan{}, tw.answer(ReadPlan{}))
			if err != nil {
				t.Fatal(err)
			}
			if msg := posPanicOf(func() { posRule(t, name).Plan(strict, keys, tw.now) }); !strings.Contains(msg, "did not load") {
				t.Fatalf("%s in a test build: %q", name, msg)
			}
		})
	}
}

// posPanicOf is what f panics with, "" when it does not.
func posPanicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

// A plan reads the sprint keys only when its read asked for them (1.5.2): the
// jopen of G, the dropping marks, the jopen of the sprint and the version of the
// stream set. The twin answers what a plan asks and nothing else, so a read that
// leaves one out is a plan that panics where it reads it; each case names the
// key dropped from the read and what the plan reads it for (finding: the read
// plans of R3 and R15 named neither jopen, and no test could tell).
func TestPlansReadOnlyWhatTheirReadAsked(t *testing.T) {
	t.Parallel()
	needsTwin := func() *posTwin {
		tw := newPosTwin("s1")
		tw.card("w", "s1", Waiting, 1, "open", "1", "needs", "n")
		tw.card("n", "s1", Landed, 0)
		tw.waiter("n", "w")
		return tw
	}
	cases := []struct {
		name, rule, key, strip string
		twin                   func() *posTwin
	}{
		{"R3 jopen of G", ruleResolve, "resolve:s1", KeyJOpenG, posResolveTwin},
		{"R3 the dropping marks", ruleResolve, "resolve:s1", KeyDropping, posResolveTwin},
		{"R4 the dropping marks", ruleNeeds, "needs:n", KeyDropping, needsTwin},
		{"R5 the dropping marks", ruleCross, "cross", KeyDropping, posCrossTwin},
		{"R15 jopen of the sprint", ruleDone, "done", KeyJOpenSprint, func() *posTwin { return posDoneTwin(3, false) }},
		{"R15 the version of the stream set", ruleDone, "done", KeyNextStreams, func() *posTwin { return posDoneTwin(3, false) }},
		{"R19 the dropping marks", posPullbackRule, "pullback:s1", KeyDropping, func() *posTwin {
			tw := newPosTwin("s1")
			tw.sentinel("g", "s1", 1)
			tw.card("r", "s1", Ready, 2)
			return tw
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tw, keys := c.twin(), []AgendaKey{posKeyOf(c.key)}
			r := posRule(t, c.rule)
			rp, _ := r.Read(keys, posBounds, 0)
			asked := 0
			for i := range rp.Sprint {
				for _, k := range rp.Sprint[i].Keys {
					asked += map[bool]int{true: 1}[k == c.strip]
				}
			}
			if asked == 0 {
				t.Fatalf("the read of %s does not ask for %s", c.rule, c.strip)
			}
			// the plan on the read as it is runs clean
			if p := r.Plan(tw.load(t, rp), keys, tw.now); len(p.Done)+len(p.Requeue)+len(p.HeldBack) == 0 {
				t.Fatalf("the plan settled no key: %+v", p)
			}
			// the same read without the key: the plan refuses at the read it makes
			for i := range rp.Sprint {
				var kept []string
				for _, k := range rp.Sprint[i].Keys {
					if k != c.strip {
						kept = append(kept, k)
					}
				}
				rp.Sprint[i].Keys = kept
			}
			s := tw.load(t, rp)
			if msg := posPanicOf(func() { r.Plan(s, keys, tw.now) }); !strings.Contains(msg, "did not load") {
				t.Fatalf("the plan without %s in its read: %q", c.strip, msg)
			}
		})
	}
}

func TestPositionReadsFitBounds(t *testing.T) {
	t.Parallel()
	const n = 2000 // more keys than a read holds queries (1,024)
	streams := make([]AgendaKey, n)
	needs := make([]AgendaKey, n)
	pulls := make([]AgendaKey, n)
	for i := range streams {
		streams[i] = AgendaKey{Key: "resolve:s" + strconv.Itoa(i), Seq: uint64(i + 1)}
		needs[i] = AgendaKey{Key: "needs:c" + strconv.Itoa(i), Seq: uint64(i + 1)}
		pulls[i] = AgendaKey{Key: "pullback:s" + strconv.Itoa(i), Seq: uint64(i + 1)}
	}
	needs = append(needs, AgendaKey{Key: "needs@5", Seq: 5}, AgendaKey{Key: "made@6+300", Seq: 6}, AgendaKey{Key: "made:m+w40", Seq: 7})
	cases := []struct {
		rule string
		keys []AgendaKey
	}{{ruleResolve, streams}, {ruleNeeds, needs}, {posPullbackRule, pulls}, {ruleCross, []AgendaKey{posKeyOf("cross")}}, {ruleDone, []AgendaKey{posKeyOf("done")}}}
	for _, c := range cases {
		for halvings := 0; halvings <= posMaxHalvings+1; halvings++ {
			rp, left := posRule(t, c.rule).Read(c.keys, posBounds, halvings)
			var used Cost
			for _, q := range rp.Sprint {
				used = used.Add(QueryCost(q))
			}
			if len(rp.Sprint) == 0 || used.Records > posBounds.Records || used.RangeIDs > posBounds.RangeIDs || used.Bytes > posBounds.Bytes ||
				len(rp.Sprint) > posBounds.Queries {
				t.Fatalf("%s at %d halvings: %d queries cost %+v", c.rule, halvings, len(rp.Sprint), used)
			}
			if c.rule == ruleResolve || c.rule == posPullbackRule || c.rule == ruleNeeds {
				if len(rp.Sprint)+len(left) != len(c.keys) {
					t.Fatalf("%s at %d halvings: %d read + %d left != %d", c.rule, halvings, len(rp.Sprint), len(left), len(c.keys))
				}
			}
		}
	}
	// a key that does not fit is left for the next tick, and the first always goes
	for _, rule := range []string{ruleResolve, posPullbackRule} {
		keys := streams
		if rule == posPullbackRule {
			keys = pulls
		}
		rp, left := posRule(t, rule).Read(keys, ReadBounds{Queries: 1, Records: 1, RangeIDs: 1, Bytes: 1}, 0)
		if len(rp.Sprint) != 1 || len(left) != n-1 || rp.Sprint[0].Stream != "s0" {
			t.Fatalf("%s under a bound of one: %d read, %d left", rule, len(rp.Sprint), len(left))
		}
	}
}

// R3's step moves at most one chunk in all: the streams of a read share it, so
// every stream's key is read, none is starved by the first, and what the read
// can hand the plan is one chunk whatever the number of streams.
func TestResolveStreamsShareOneChunk(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 10, 250} {
		keys := make([]AgendaKey, n)
		for i := range keys {
			keys[i] = AgendaKey{Key: "resolve:s" + strconv.Itoa(i), Seq: uint64(i + 1)}
		}
		rp, left := posRule(t, ruleResolve).Read(keys, posBounds, 0)
		heads := 0
		for _, q := range rp.Sprint {
			heads += q.Heads[0].Limit
		}
		if len(left) != 0 || len(rp.Sprint) != n || heads > positionChunk || heads < positionChunk-n {
			t.Fatalf("%d streams: %d read, %d left, heads of %d in all, the chunk is %d", n, len(rp.Sprint), len(left), heads, positionChunk)
		}
	}
	// the plan of 250 streams with cards to release moves one chunk in all
	rows := make([]string, 250)
	for i := range rows {
		rows[i] = "s" + strconv.Itoa(i)
	}
	tw := newPosTwin(rows...)
	keys := make([]AgendaKey, len(rows))
	for i, r := range rows {
		for j := 0; j < 30; j++ {
			tw.card(r+"-"+strconv.Itoa(j), r, Waiting, float64(j))
		}
		keys[i] = AgendaKey{Key: "resolve:" + r, Seq: uint64(i + 1)}
	}
	p, out := tw.run(t, ruleResolve, 0, keys...)
	if out.refused != "" || len(p.Plan.Units) > positionChunk || len(p.Plan.Units) < positionChunk-len(rows) {
		t.Fatalf("250 streams moved %d cards in one plan, the chunk is %d: %+v", len(p.Plan.Units), positionChunk, out)
	}
	if len(p.Requeue) != len(rows) {
		t.Fatalf("every stream still has cards to release: requeue %d", len(p.Requeue))
	}
	// and every stream is served in a few runs, none starved
	if runs := tw.drain(t, ruleResolve, 0, 20); runs > 8 {
		t.Fatalf("250 streams of 30 cards took %d more runs", runs)
	}
	for _, r := range tw.work {
		if r.col != Ready {
			t.Fatalf("%s is still %s", r.id, r.col)
		}
	}
}

// Every rule read names its fields (1.0, F1-12): the queries that read records
// project them. The waiters of a need are read for their place, which no field
// carries, and IT05's shapes cannot say "no field" (an empty list is whole
// records): the read names the one the intents change.
func TestPositionReadsNameTheirFields(t *testing.T) {
	t.Parallel()
	for name, key := range map[string]string{ruleResolve: "resolve:s1", ruleCross: "cross", ruleDone: "done", posPullbackRule: "pullback:s1", ruleNeeds: "needs:n"} {
		rp, _ := posRule(t, name).Read([]AgendaKey{posKeyOf(key)}, posBounds, 0)
		if len(rp.Sprint) == 0 {
			t.Fatalf("%s reads nothing", name)
		}
		for _, q := range rp.Sprint {
			if len(q.Fields) == 0 {
				t.Fatalf("%s reads %s with no projection", name, q.Kind)
			}
		}
	}
}

func TestResolveReadHalvesItsHead(t *testing.T) {
	t.Parallel()
	k := []AgendaKey{posKeyOf("resolve:s1")}
	// IT05's Halved: each halving takes the larger half, never below one
	want := []int{2000, 1000, 500, 250, 125, 63, 32, 16, 8, 4, 2, 1, 1}
	for h, w := range want {
		rp, _ := posRule(t, ruleResolve).Read(k, posBounds, h)
		if got := rp.Sprint[0].Heads[0].Limit; got != w {
			t.Fatalf("%d halvings: head %d, want %d", h, got, w)
		}
	}
}

func TestNeedsReadSizesTheHead(t *testing.T) {
	t.Parallel()
	// the heads of the needs of a read fit layer 1's records: 100 single needs
	// read 99 waiters each, and a line of a landing reads its next window
	single := make([]AgendaKey, 100)
	for i := range single {
		single[i] = AgendaKey{Key: "needs:n" + strconv.Itoa(i), Seq: uint64(i)}
	}
	rp, left := posRule(t, ruleNeeds).Read(single, posBounds, 0)
	q := rp.Sprint[0]
	if len(left) != 0 || len(rp.Sprint) != 100 || q.Limit != 99 || q.Source.Kind != SourceIDs || !reflect.DeepEqual(q.Source.IDs, []string{"n0"}) ||
		q.Missing || q.WaiterAfter != "" || !reflect.DeepEqual(q.Keys, []string{KeyDropping}) {
		t.Fatalf("100 needs: %d queries, %+v, %d left", len(rp.Sprint), q, len(left))
	}
	// many more keys than a read holds: the rest wait, and the head never goes
	// below the floor
	many := make([]AgendaKey, 5000)
	for i := range many {
		many[i] = AgendaKey{Key: "needs:n" + strconv.Itoa(i), Seq: uint64(i)}
	}
	rp, left = posRule(t, ruleNeeds).Read(many, posBounds, 0)
	if len(rp.Sprint) == 0 || len(rp.Sprint)+len(left) != len(many) || rp.Sprint[0].Limit < needsMinWaiters {
		t.Fatalf("5,000 needs: %d queries, head %d, %d left", len(rp.Sprint), rp.Sprint[0].Limit, len(left))
	}
	rp, _ = posRule(t, ruleNeeds).Read([]AgendaKey{{Key: "made@41+200", Seq: 41}}, posBounds, 0)
	q = rp.Sprint[0]
	if src := q.Source; src.Kind != SourceLine || src.Seq != 41 || src.Offset != 200 || src.Limit != needsLineWindow || !q.Missing || q.Limit != 99 {
		t.Fatalf("a made line: %+v", q)
	}
	// a made need carried past its first waiters reads the head after the last
	// one served
	rp, _ = posRule(t, ruleNeeds).Read([]AgendaKey{{Key: "made:n+w150", Seq: 41}}, posBounds, 0)
	q = rp.Sprint[0]
	if q.Source.Kind != SourceIDs || !reflect.DeepEqual(q.Source.IDs, []string{"n"}) || q.WaiterAfter != "w150" || !q.Missing || q.Limit != 2000 {
		t.Fatalf("a made need from after w150: %+v", q)
	}
	// the read is sized within the bounds by its declared cost, bytes included
	for _, b := range []ReadBounds{{Queries: 1024, Records: 10000, RangeIDs: 20000, Bytes: 1 << 20}, {Queries: 4, Records: 500, RangeIDs: 400, Bytes: 8 << 20}} {
		rp, _ := posRule(t, ruleNeeds).Read(single, b, 0)
		var used Cost
		for _, q := range rp.Sprint {
			used = used.Add(QueryCost(q))
		}
		if len(rp.Sprint) == 0 || len(rp.Sprint) > b.Queries || used.Records > b.Records || used.RangeIDs > b.RangeIDs || used.Bytes > b.Bytes {
			t.Fatalf("within %+v: %d queries cost %+v", b, len(rp.Sprint), used)
		}
	}
}

func TestPositionKeysSplit(t *testing.T) {
	t.Parallel()
	good := []struct {
		key  string
		want posKey
	}{
		{"cross", posKey{rule: "cross"}},
		{"resolve:s1", posKey{rule: "resolve", subject: "s1"}},
		{"needs@48213", posKey{rule: "needs", line: 48213, byLine: true}},
		{"made@7+2000", posKey{rule: "made", line: 7, offset: 2000, byLine: true}},
		{"needs:n1", posKey{rule: "needs", subject: "n1"}},
		{"made:n1", posKey{rule: "made", subject: "n1"}},
		{"made:n1+w150", posKey{rule: "made", subject: "n1", after: "w150"}},
		{"made:n1+c-w.3", posKey{rule: "made", subject: "n1", after: "c-w.3"}},
		{"made:n1+7", posKey{rule: "made", subject: "n1", after: "7"}},
	}
	for _, g := range good {
		got, ok := posSplitKey(AgendaKey{Key: g.key})
		if !ok || got != g.want {
			t.Fatalf("%s: %+v %v", g.key, got, ok)
		}
	}
	for _, bad := range []string{"", "needs:", "needs@", "needs@x", "needs@0", "needs@5+", "needs@5+-1", "needs@5+x",
		"made:n+", "made:+w3", "made:n+w+1", "made:n+a:b", "made:n+a@b", "needs:n+w3", "resolve:s1+w2", "pullback:s1+w1"} {
		if _, ok := posSplitKey(AgendaKey{Key: bad}); ok {
			t.Fatalf("%q was read as a key", bad)
		}
	}
	for _, k := range []AgendaKey{{Key: "needs@9", Seq: 9}, {Key: "made@9+100", Seq: 9}} {
		p, _ := posSplitKey(k)
		if again := posLineKey(p.rule, p.line, p.offset, k.Seq); again != k {
			t.Fatalf("%v put back together as %v", k, again)
		}
	}
	for _, k := range []AgendaKey{{Key: "needs:n", Seq: 9}, {Key: "made:n", Seq: 9}, {Key: "made:n+w3", Seq: 9}} {
		p, _ := posSplitKey(k)
		if again := posNeedKey(p.rule, p.subject, p.after, k.Seq); again != k {
			t.Fatalf("%v put back together as %v", k, again)
		}
	}
}

func TestSetGuardRidesInRulePlan(t *testing.T) {
	t.Parallel()
	in := []SetGuard{
		posBefore("s1", 12.5, nil, posInt(0)),
		posNoSentinelUpTo("s1", 1e6),
		{Kind: GuardRCount, Table: Work, Cells: posOpenCells("a", "b"), Min: "-inf", Max: "+inf", AtLeast: posInt(1)},
	}
	var p RulePlan
	p.Guards = append(p.Guards, XGuard{Kind: "memberup", Member: "m"})
	for _, g := range in {
		p.Guards = append(p.Guards, g.XGuard())
	}
	got, err := SetGuardsOf(p)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	p.Guards = append(p.Guards, XGuard{Kind: posSetGuard, Key: "{"})
	if _, err := SetGuardsOf(p); err == nil {
		t.Fatal("a guard that does not decode was dropped")
	}
}

// A plan reads the partial snapshot only through what its read loaded, never a
// cell (1.5.2): none of the scans of the scanning tick appear in the rules.
func TestPositionRulesNeverScanACell(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"rules_position.go", "rules_position_read.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, scan := range []string{".Cell(", ".Column(", ".Of(", "openLine(", "StopBefore(", "PositionWaits(", "Behind(", "WaitsFor(", "NamedWaits(", "Lawful(", "s.Open", "s.Acked"} {
			for i, line := range strings.Split(string(src), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if strings.Contains(line, scan) {
					t.Errorf("%s:%d scans with %s: %s", file, i+1, scan, strings.TrimSpace(line))
				}
			}
		}
	}
}

// Every rule of this file is idempotent (E7, 1.3.3): run once, applied, and run
// again on the same keys with nothing changed, the second step is empty.
func TestPositionRulesTwiceSecondEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rule  string
		build func() (*posTwin, AgendaKey)
	}{
		{ruleResolve, func() (*posTwin, AgendaKey) { return posResolveTwin(), posKeyOf("resolve:s1") }},
		{ruleResolve, func() (*posTwin, AgendaKey) { // a reach
			tw := newPosTwin("s1")
			tw.card("d1", "s1", Landed, 1)
			tw.sentinel("g", "s1", 5)
			return tw, posKeyOf("resolve:s1")
		}},
		{ruleNeeds, func() (*posTwin, AgendaKey) {
			tw := newPosTwin("s1")
			tw.card("w", "s1", Waiting, 1, "open", "2", "needs", "a,b")
			tw.card("a", "s1", Landed, 0)
			tw.card("b", "s1", Landed, 0)
			tw.waiter("a", "w")
			tw.waiter("b", "w")
			tw.lines[3] = []string{"a", "b"}
			return tw, AgendaKey{Key: "needs@3", Seq: 3}
		}},
		{ruleNeeds, func() (*posTwin, AgendaKey) { // gone
			tw := newPosTwin("s1")
			tw.card("w", "s1", Waiting, 1, "open", "1", "needs", "n")
			tw.work["n"] = &posRec{id: "n", rev: 1, f: map[string]string{}}
			tw.waiter("n", "w")
			return tw, posKeyOf("needs:n")
		}},
		{ruleNeeds, func() (*posTwin, AgendaKey) { // made
			tw := newPosTwin("s1")
			tw.card("w", "s1", Waiting, 1, "open", "1", "needs", "n")
			tw.card("n", "s1", Waiting, 0)
			tw.waiter("n", "w")
			tw.missing["n"] = true
			tw.judge(NMissingNeed, "n", "w")
			return tw, posKeyOf("made:n")
		}},
		{ruleCross, func() (*posTwin, AgendaKey) { return posCrossTwin(), posKeyOf("cross") }},
		{ruleDone, func() (*posTwin, AgendaKey) { return posDoneTwin(4, false), posKeyOf("done") }},
		{ruleDone, func() (*posTwin, AgendaKey) { // a close
			tw := posDoneTwin(4, true)
			tw.judge(NSprintDone, "", SprintSubject)
			return tw, posKeyOf("done")
		}},
		{ruleResolve, func() (*posTwin, AgendaKey) { // a release and an unreach in one plan
			tw := newPosTwin("s1")
			tw.sentinel("g", "s1", 5)
			tw.card("a", "s1", Waiting, 1)
			tw.judge(NSentinelReached, "", "g")
			return tw, posKeyOf("resolve:s1")
		}},
		{ruleNeeds, func() (*posTwin, AgendaKey) { // two missing needs of one waiter, made by one line
			tw := newPosTwin("s1", "s2")
			tw.card("w", "s1", Waiting, 1, "open", "2", "needs", "n1,n2")
			for _, n := range []string{"n1", "n2"} {
				tw.card(n, "s2", Waiting, 0)
				tw.waiter(n, "w")
				tw.missing[n] = true
				tw.judge(NMissingNeed, n, "w")
			}
			tw.lines[11] = []string{"n1", "n2"}
			return tw, AgendaKey{Key: "made@11", Seq: 11}
		}},
		{ruleCross, func() (*posTwin, AgendaKey) { // two cross streams and one stopped for a conflict
			tw := posCrossTwin()
			tw.rows = append(tw.rows, "s4", "s5")
			tw.streams += 2
			tw.ctl("s4", "state", StreamStopped, "cause", "cross", "other", "x", "card", "m9")
			tw.stuck("s4", "m9", 1)
			tw.ctl("s5", "state", StreamStopped, "cause", "conflict", "other", "x", "card", "m8")
			tw.stuck("s5", "m8", 1)
			return tw, posKeyOf("cross")
		}},
		{ruleDone, func() (*posTwin, AgendaKey) { // one card landed, three dropped
			tw := newPosTwin("s1", "s2")
			tw.card("a", "s1", Landed, 1)
			tw.ctl("s1", "dropped", "1")
			tw.ctl("s2", "dropped", "2")
			return tw, posKeyOf("done")
		}},
		{posPullbackRule, func() (*posTwin, AgendaKey) { // cards below and above σ, and a working one
			tw := newPosTwin("s1")
			tw.card("below", "s1", Waiting, 0)
			tw.sentinel("g", "s1", 1)
			tw.card("work", "s1", Working, 0.5)
			for i := 0; i < 4; i++ {
				tw.card("r"+strconv.Itoa(i), "s1", Ready, float64(2+i))
			}
			return tw, posKeyOf("pullback:s1")
		}},
		{ruleResolve, func() (*posTwin, AgendaKey) { // an unreach of a held judgment
			tw := newPosTwin("s1")
			tw.sentinel("g", "s1", 5)
			tw.card("in", "s1", Ready, 3)
			tw.hold(NSentinelReached, "", "g")
			return tw, posKeyOf("resolve:s1")
		}},
		{ruleDone, func() (*posTwin, AgendaKey) { // every card dropped
			tw := newPosTwin("s1", "s2")
			tw.ctl("s1", "dropped", "2")
			tw.ctl("s2", "dropped", "1")
			return tw, posKeyOf("done")
		}},
		{posPullbackRule, func() (*posTwin, AgendaKey) {
			tw := newPosTwin("s1")
			tw.sentinel("g", "s1", 1)
			for i := 0; i < 10; i++ {
				tw.card("r"+strconv.Itoa(i), "s1", Ready, float64(2+i))
			}
			return tw, posKeyOf("pullback:s1")
		}},
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%s-%d", c.rule, i), func(t *testing.T) {
			t.Parallel()
			tw, k := c.build()
			first, out := tw.run(t, c.rule, 0, k)
			if out.refused != "" || !out.wrote {
				t.Fatalf("the first run had nothing to do: %+v %+v", out, first)
			}
			second, out := tw.run(t, c.rule, 0, k)
			if out.refused != "" || out.wrote || !posQuiet(second) {
				t.Fatalf("the second run is not empty: %+v %+v", out, second)
			}
		})
	}
}

// Benchmarks: the plan of each rule at the size of a step, in Go time. The
// store's time (a release of 2,000 in 25 ms, front(s) in 1 ms) is IT25's.

func benchPlan(b *testing.B, name string, tw *posTwin, key AgendaKey) {
	b.Helper()
	r := RuleTable()
	var rule Rule
	for _, x := range r {
		if x.Name == name {
			rule = x
		}
	}
	rp, _ := rule.Read([]AgendaKey{key}, posBounds, 0)
	s := tw.load(b, rp)
	keys := []AgendaKey{key}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p := rule.Plan(s, keys, tw.now); len(p.Done)+len(p.Requeue) != 1 {
			b.Fatal("no key settled")
		}
	}
}

func BenchmarkPlanRelease2000(b *testing.B) {
	tw := newPosTwin("s1")
	for i := 0; i < 2000; i++ {
		tw.card("c"+strconv.Itoa(i), "s1", Waiting, float64(i))
	}
	benchPlan(b, ruleResolve, tw, posKeyOf("resolve:s1"))
}

func BenchmarkPlanPullback2000(b *testing.B) {
	tw := newPosTwin("s1")
	tw.sentinel("g", "s1", -1)
	for i := 0; i < 2000; i++ {
		tw.card("c"+strconv.Itoa(i), "s1", Ready, float64(i))
	}
	benchPlan(b, posPullbackRule, tw, posKeyOf("pullback:s1"))
}

func BenchmarkPlanNeeds2000(b *testing.B) {
	tw := newPosTwin("s1")
	tw.card("n", "s1", Landed, 0)
	for i := 0; i < 2000; i++ {
		id := "w" + strconv.Itoa(i)
		tw.card(id, "s1", Waiting, float64(i), "open", "1", "needs", "n")
		tw.waiter("n", id)
	}
	benchPlan(b, ruleNeeds, tw, posKeyOf("needs:n"))
}

func BenchmarkPlanDone250Streams(b *testing.B) {
	benchPlan(b, ruleDone, posDoneTwin(250, false), posKeyOf("done"))
}

// R4 on a line and on a need with more waiters than a read holds.

// A line key moves its offset past a need whose only remaining waiters are in
// dropping streams (2.3 R4: the offset moves past n once its set is empty of
// waiters outside dropping streams), and the frozen waiters stay owed by a key
// of their own, held back until the mark clears (1.3.5: work skipped for a drop
// is never forgotten). Before, the key stopped at that need, and the waiters of
// the needs after it were never served while the mark stood.
func TestNeedsLineMovesPastANeedWithOnlyFrozenWaiters(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = "n" + strconv.Itoa(i)
		tw.card(ids[i], "s1", Landed, float64(i))
	}
	tw.lines[21] = ids
	tw.card("frozen", "s2", Waiting, 1000, "open", "1", "needs", "n0")
	tw.waiter("n0", "frozen")
	tw.card("live", "s1", Waiting, 1001, "open", "1", "needs", "n120")
	tw.waiter("n120", "live")
	tw.dropping["s2"] = true
	k := AgendaKey{Key: "needs@21", Seq: 21}
	tw.agenda[k.Key] = k.Seq

	p, out := tw.run(t, ruleNeeds, 0, k)
	if out.refused != "" || len(p.Intents) != 0 {
		t.Fatalf("first window: %+v %+v", out, p.Intents)
	}
	posSameStrings(t, "done", keyTexts(p.Done), []string{"needs@21"})
	posSameStrings(t, "requeue", keyTexts(p.Requeue), []string{"needs:n0", "needs@21+100"})
	posSameStrings(t, "held back", keyTexts(p.HeldBack), []string{"needs:n0"})
	for _, key := range p.Requeue {
		if key.Seq != 21 {
			t.Fatalf("%s lost the order of the key it carries: %d", key.Key, key.Seq)
		}
	}
	// the second window serves n120's waiter while n0's stays frozen
	tw.drain(t, ruleNeeds, 0, 5)
	if tw.work["live"].f["open"] != "0" || tw.work["frozen"].f["open"] != "1" || !tw.wait["n0"]["frozen"] {
		t.Fatalf("live open %s, frozen open %s", tw.work["live"].f["open"], tw.work["frozen"].f["open"])
	}
	if _, ok := tw.agenda["needs:n0"]; !ok || !tw.heldKeys["needs:n0"] || len(tw.agenda) != 1 {
		t.Fatalf("the frozen waiter is owed by nothing: agenda %v held %v", tw.agenda, tw.heldKeys)
	}
	// the mark clears: the held key is planned again and serves it
	delete(tw.dropping, "s2")
	tw.heldKeys = map[string]bool{}
	tw.drain(t, ruleNeeds, 0, 5)
	if tw.work["frozen"].f["open"] != "0" || len(tw.agenda) != 0 {
		t.Fatalf("after the mark cleared: open %s, agenda %v", tw.work["frozen"].f["open"], tw.agenda)
	}
}

// A need's whole head in dropping streams, with waiters behind it: the head of
// wait:n is the only place a waiter is read from, so the ones behind wait until
// the mark clears, and the key stays for all of them. The design does not say
// how the head moves past frozen waiters (a question of the pull request).
func TestNeedsFrozenHeadHoldsTheKey(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.card("n", "s1", Landed, 0)
	tw.card("w1", "s2", Waiting, 1, "open", "1", "needs", "n")
	tw.card("w2", "s1", Waiting, 2, "open", "1", "needs", "n")
	tw.waiter("n", "w1", "w2")
	tw.dropping["s2"] = true
	k := posKeyOf("needs:n")
	tw.agenda[k.Key] = k.Seq
	p, out := tw.run(t, ruleNeeds, posMaxHalvings, k) // a head of one waiter
	if out.refused != "" || len(p.Intents) != 0 || !posHas(p.HeldBack, k.Key) || len(p.Done) != 0 || len(p.Requeue) != 0 {
		t.Fatalf("plan: %+v %+v", p, out)
	}
	delete(tw.dropping, "s2")
	tw.heldKeys = map[string]bool{}
	if runs := tw.drain(t, ruleNeeds, posMaxHalvings, 5); runs != 2 || tw.work["w1"].f["open"] != "0" || tw.work["w2"].f["open"] != "0" {
		t.Fatalf("after the mark cleared: %d runs, open %s %s", runs, tw.work["w1"].f["open"], tw.work["w2"].f["open"])
	}
}

// posWaiterID is the id of the i-th waiter of a made need: the ids sort as i
// does, so the order of wait:n (by id) is the order the tests count in.
func posWaiterID(i int) string { return fmt.Sprintf("w%03d", i) }

func posMadeTwin(waiters int) *posTwin {
	tw := newPosTwin("s1", "s2")
	tw.card("n", "s2", Waiting, 0)
	tw.missing["n"] = true
	for i := 0; i < waiters; i++ {
		id := posWaiterID(i)
		tw.card(id, "s1", Waiting, float64(10+i), "open", "1", "needs", "n")
		tw.waiter("n", id)
		tw.judge(NMissingNeed, "n", id)
	}
	return tw
}

func (tw *posTwin) openMissing() int {
	n := 0
	for k := range tw.judged {
		if strings.HasPrefix(k, NMissingNeed+"\x00") {
			n++
		}
	}
	return n
}

// A made need with more waiters than one read's head closes the head's missing
// judgments, keeps n in missing and keeps the key, with an offset past the
// waiters closed, until every waiter is served; only the read that reaches the end
// of wait:n takes n out of missing. Before, n left missing after the first head
// and the waiters beyond it kept "blocked on something missing" while n existed
// (51 of 150 in the reader's check), with nothing left to close them.
func TestMadeWithMoreWaitersThanTheHeadServesThemAll(t *testing.T) {
	t.Parallel()
	tw := posMadeTwin(150)
	tw.card("x", "s2", Waiting, 1)
	tw.lines[31] = []string{"x", "n"}
	k := AgendaKey{Key: "made@31", Seq: 31}
	tw.agenda[k.Key] = k.Seq

	p, out := tw.run(t, ruleNeeds, 0, k)
	if out.refused != "" || len(p.Notes) != 1 || len(p.Notes[0].Subjects) != 99 || len(p.Intents) != 0 {
		t.Fatalf("first run: %+v notes %d intents %+v", out, len(p.Notes), p.Intents)
	}
	if !tw.missing["n"] || tw.openMissing() != 51 {
		t.Fatalf("after the first head: missing %v, %d judgments open", tw.missing, tw.openMissing())
	}
	posSameStrings(t, "done", keyTexts(p.Done), []string{"made@31"})
	posSameStrings(t, "requeue", keyTexts(p.Requeue), []string{"made:n+" + posWaiterID(98)})
	if p.Requeue[0].Seq != 31 || len(p.HeldBack) != 0 {
		t.Fatalf("the key carried: %+v held %v", p.Requeue, p.HeldBack)
	}
	p, out = tw.run(t, ruleNeeds, 0, p.Requeue[0])
	if out.refused != "" || len(p.Notes) != 1 || len(p.Notes[0].Subjects) != 51 || len(p.Intents) != 1 || p.Intents[0].Kind != posMade ||
		!posHas(p.Done, "made:n+"+posWaiterID(98)) || len(p.Requeue) != 0 {
		t.Fatalf("second run: %+v notes %+v intents %+v done %v requeue %v", out, p.Notes, p.Intents, p.Done, p.Requeue)
	}
	if tw.missing["n"] || tw.openMissing() != 0 || len(tw.agenda) != 0 {
		t.Fatalf("after the last head: missing %v, %d open, agenda %v", tw.missing, tw.openMissing(), tw.agenda)
	}
	// the waiters stay in wait:n with open unchanged: they wait for n to land
	for i := 0; i < 150; i++ {
		w := posWaiterID(i)
		if !tw.wait["n"][w] || tw.work[w].f["open"] != "1" {
			t.Fatalf("%s did not stay waiting", w)
		}
	}
	if q := tw.plan(t, ruleNeeds, 0, k); !posQuiet(q) {
		t.Fatalf("a second plan of the line: %+v", q)
	}
}

// The same at a head of one waiter: the key moves one waiter a run, n stays in
// missing until the last, and a run repeated on a key already served closes
// nothing twice.
func TestMadeHeadOfOneClosesOneWaiterARun(t *testing.T) {
	t.Parallel()
	tw := posMadeTwin(5)
	k := posKeyOf("made:n")
	tw.agenda[k.Key] = k.Seq
	for run := 1; run <= 5; run++ {
		keys := keyTexts([]AgendaKey{})
		for key := range tw.agenda {
			keys = append(keys, key)
		}
		if len(keys) != 1 {
			t.Fatalf("run %d: agenda %v", run, tw.agenda)
		}
		p, out := tw.run(t, ruleNeeds, posMaxHalvings, AgendaKey{Key: keys[0], Seq: tw.agenda[keys[0]]})
		if out.refused != "" || len(p.Notes) != 1 || len(p.Notes[0].Subjects) != 1 || p.Notes[0].Subjects[0] != posWaiterID(run-1) {
			t.Fatalf("run %d: %+v %+v", run, out, p.Notes)
		}
		last := run == 5
		if last == tw.missing["n"] || tw.openMissing() != 5-run || last != (len(p.Intents) == 1) {
			t.Fatalf("run %d: missing %v, %d open, intents %+v", run, tw.missing, tw.openMissing(), p.Intents)
		}
		want := "made:n+" + posWaiterID(run-1)
		if !last && (len(p.Requeue) != 1 || p.Requeue[0].Key != want || !posHas(p.Done, keys[0])) {
			t.Fatalf("run %d: requeue %v done %v, want %s", run, p.Requeue, p.Done, want)
		}
	}
	if len(tw.agenda) != 0 {
		t.Fatalf("agenda %v", tw.agenda)
	}
	if q := tw.plan(t, ruleNeeds, posMaxHalvings, k); !posQuiet(q) {
		t.Fatalf("the key again: %+v", q)
	}
}

// A waiter of a dropping stream in the head of a made need holds the need whole:
// nothing is closed and the key stays, held at its offset, until the mark clears.
func TestMadeFrozenWaiterInTheHeadHoldsTheNeedAtItsOffset(t *testing.T) {
	t.Parallel()
	tw := posMadeTwin(3)
	tw.work[posWaiterID(1)].row = "s2"
	tw.dropping["s2"] = true
	k := posKeyOf("made:n")
	tw.agenda[k.Key] = k.Seq
	tw.drain(t, ruleNeeds, posMaxHalvings, 6)
	// w0 was closed, and the key stands held at the frozen waiter
	if !tw.missing["n"] || tw.openMissing() != 2 || tw.opened(NMissingNeed, "n", posWaiterID(0)) {
		t.Fatalf("missing %v, %d open", tw.missing, tw.openMissing())
	}
	if held := "made:n+" + posWaiterID(0); !tw.heldKeys[held] || len(tw.agenda) != 1 || tw.agenda[held] == 0 {
		t.Fatalf("agenda %v held %v", tw.agenda, tw.heldKeys)
	}
	delete(tw.dropping, "s2")
	tw.heldKeys = map[string]bool{}
	tw.drain(t, ruleNeeds, posMaxHalvings, 6)
	if tw.missing["n"] || tw.openMissing() != 0 || len(tw.agenda) != 0 {
		t.Fatalf("after the mark cleared: missing %v, %d open, agenda %v", tw.missing, tw.openMissing(), tw.agenda)
	}
}

// A waiter that leaves wait:n between two heads of a made need moves nobody: the
// key carries the last waiter served, a place in the order of wait:n, and not a
// count of the waiters closed. Before, the offset was a count: with a closed
// waiter gone from wait:n the next head began one waiter late, n left missing
// with that waiter still blocked on it (the second cold read of #4752). n leaves
// missing only when the cursor has passed every waiter, whichever waiters left.
func TestMadeWaiterLeavingBetweenTwoHeadsSkipsNobody(t *testing.T) {
	t.Parallel()
	const waiters, halvings = 50, 6
	cases := []struct {
		name  string
		leave func(head int) []int // the waiters that leave wait:n between the heads, by place
	}{
		{"one already closed", func(head int) []int { return []int{0} }},
		{"the one the cursor names", func(head int) []int { return []int{head - 1} }},
		{"the whole first head", func(head int) []int {
			var out []int
			for i := 0; i < head; i++ {
				out = append(out, i)
			}
			return out
		}},
		{"one not yet served", func(head int) []int { return []int{head + 3} }},
		{"one closed and one not yet served", func(head int) []int { return []int{0, head + 3} }},
	}
	for _, c := range cases {
		for _, start := range []string{"made:n", "made@31"} {
			t.Run(c.name+"/"+start, func(t *testing.T) {
				t.Parallel()
				tw := posMadeTwin(waiters)
				tw.card("x", "s2", Waiting, 1)
				tw.lines[31] = []string{"x", "n"}
				k := AgendaKey{Key: start, Seq: 31}
				tw.agenda[k.Key] = k.Seq
				p, out := tw.run(t, ruleNeeds, halvings, k)
				if out.refused != "" || len(p.Notes) != 1 || len(p.Requeue) != 1 {
					t.Fatalf("first head: %+v notes %d requeue %v", out, len(p.Notes), p.Requeue)
				}
				head := len(p.Notes[0].Subjects)
				if head < 4 || head+4 > waiters {
					t.Fatalf("a head of %d does not leave waiters beyond it", head)
				}
				if want := "made:n+" + posWaiterID(head-1); p.Requeue[0].Key != want {
					t.Fatalf("the key carried is %s, want %s: the last waiter served", p.Requeue[0].Key, want)
				}
				// waiters leave wait:n between the heads: a card leaving waiting closes
				// its judgments and is taken from every wait:n it is in (1.3.3)
				for _, i := range c.leave(head) {
					id := posWaiterID(i)
					delete(tw.wait["n"], id)
					delete(tw.judged, posJKey(NMissingNeed, "n", id))
				}
				for run := 2; ; run++ {
					if run > waiters || len(tw.agenda) != 1 {
						t.Fatalf("run %d: agenda %v", run, tw.agenda)
					}
					var key AgendaKey
					for text, seq := range tw.agenda {
						key = AgendaKey{Key: text, Seq: seq}
					}
					p, out := tw.run(t, ruleNeeds, halvings, key)
					if out.refused != "" {
						t.Fatalf("run %d: %+v", run, out)
					}
					if len(p.Requeue) == 0 {
						// the last head: the cursor has passed every waiter
						if tw.missing["n"] || len(tw.agenda) != 0 {
							t.Fatalf("run %d is the last: missing %v agenda %v", run, tw.missing, tw.agenda)
						}
						break
					}
					if !tw.missing["n"] {
						t.Fatalf("run %d left missing with more waiters beyond its head: %v", run, p.Requeue)
					}
				}
				for id := range tw.wait["n"] {
					if tw.opened(NMissingNeed, "n", id) {
						t.Fatalf("%s is still blocked on a missing n", id)
					}
				}
				if n := tw.openMissing(); n != 0 {
					t.Fatalf("%d judgments still open", n)
				}
			})
		}
	}
}

// A made key cut at its head and delivered a second time plans the close of its
// first head again and requeues the same carried key: the waiters stay in wait:n
// until the need is made, so the head as read is the same. It writes nothing (J
// drops the close of a judgment that is not open), puts no second key in the
// agenda, and the need is still served to the end (E7, the second delivery of a
// key).
func TestMadeCutKeyDeliveredTwiceWritesNothing(t *testing.T) {
	t.Parallel()
	tw := posMadeTwin(50)
	k := posKeyOf("made:n")
	tw.agenda[k.Key] = k.Seq
	first, out := tw.run(t, ruleNeeds, 6, k)
	if out.refused != "" || !out.wrote || len(first.Requeue) != 1 {
		t.Fatalf("first delivery: %+v requeue %v", out, first.Requeue)
	}
	agenda := maps.Clone(tw.agenda)
	judged := maps.Clone(tw.judged)
	_, out = tw.run(t, ruleNeeds, 6, k)
	if out.refused != "" || out.wrote || !maps.Equal(tw.agenda, agenda) || !maps.Equal(tw.judged, judged) || !tw.missing["n"] {
		t.Fatalf("second delivery: %+v agenda %v want %v", out, tw.agenda, agenda)
	}
	tw.drain(t, ruleNeeds, 6, 10)
	if tw.missing["n"] || tw.openMissing() != 0 || len(tw.agenda) != 0 {
		t.Fatalf("after the end: missing %v, %d open, agenda %v", tw.missing, tw.openMissing(), tw.agenda)
	}
}

// R4 is run on random states to the end: every waiter of a landed or removed
// need outside a dropping stream is served, none of a dropping stream is served
// while the mark stands and the work it leaves is owed by a key, every made need
// closes and leaves missing once the marks clear, the keys run out, and every key
// run again plans nothing (E7). The chunk of each run is one of the halvings.
func TestNeedsRunToTheEndOnRandomStates(t *testing.T) {
	t.Parallel()
	for seed := int64(1); seed <= 60; seed++ {
		t.Run(strconv.FormatInt(seed, 10), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			streams := []string{"s1", "s2", "s3"}
			tw := newPosTwin(streams...)
			for _, s := range streams[1:] {
				if rng.Intn(2) == 0 {
					tw.dropping[s] = true
				}
			}
			var landed, made []string
			var initial []AgendaKey
			for i := 0; i < 1+rng.Intn(12); i++ {
				n := "n" + strconv.Itoa(i)
				switch rng.Intn(4) {
				case 0:
					tw.card(n, "s1", Landed, float64(i))
					landed = append(landed, n)
				case 1:
					tw.work[n] = &posRec{id: n, rev: 1, f: map[string]string{"outcome": "dropped"}}
					landed = append(landed, n)
				case 2:
					tw.card(n, "s1", Working, float64(i)) // open: its landing queues the key again
					landed = append(landed, n)
				default:
					tw.card(n, "s1", Waiting, float64(i))
					tw.missing[n] = true
					made = append(made, n)
				}
				for w := 0; w < rng.Intn(26); w++ {
					id := n + "-w" + strconv.Itoa(w)
					tw.card(id, streams[rng.Intn(len(streams))], Waiting, float64(1000+w), "open", "1", "needs", n)
					tw.waiter(n, id)
					if tw.missing[n] {
						tw.judge(NMissingNeed, n, id)
					}
				}
			}
			if len(landed) > 0 {
				tw.lines[1] = landed
				initial = append(initial, AgendaKey{Key: "needs@1", Seq: 1})
			}
			if len(made) > 0 {
				tw.lines[2] = made
				initial = append(initial, AgendaKey{Key: "made@2", Seq: 2})
			}
			for _, k := range initial {
				tw.agenda[k.Key] = k.Seq
			}
			halvings := []int{0, 4, 8, posMaxHalvings}[rng.Intn(4)]
			frozenServed := func() {
				for n, ws := range tw.wait {
					for w := range ws {
						if r := tw.work[w]; tw.dropping[r.row] && r.f["open"] != "1" {
							t.Fatalf("%s of a dropping stream was served for %s", w, n)
						}
					}
				}
				for k := range tw.judged {
					if parts := strings.Split(k, "\x00"); parts[0] == NBlocked && tw.dropping[tw.work[parts[2]].row] {
						t.Fatalf("%s of a dropping stream was blocked", parts[2])
					}
				}
			}
			runs := tw.drain(t, ruleNeeds, halvings, 400)
			t.Logf("seed %d: %d landed or removed, %d made, %d dropping, chunk %d halvings, %d runs, %d keys held", seed, len(landed), len(made), len(tw.dropping), halvings, runs, len(tw.heldKeys))
			frozenServed()
			owed := 0
			for n, ws := range tw.wait {
				frozen := false
				for w := range ws {
					frozen = frozen || tw.dropping[tw.work[w].row]
				}
				for w := range ws {
					if r := tw.work[w]; r.f["open"] == "1" && tw.missing[n] || tw.opened(NMissingNeed, n, w) {
						owed++
					} else if p := tw.work[n].col; p == Landed || p == "" {
						owed++
						// a need with no frozen waiter is served whole; with one, the
						// waiters behind a head that is all frozen wait (a question)
						if !frozen {
							t.Fatalf("%s of %s was not served, and no waiter of it is frozen", w, n)
						}
					}
				}
			}
			if owed > 0 && len(tw.agenda) == 0 {
				t.Fatalf("%d waiters are owed and no key remains", owed)
			}
			// the marks clear, and everything is served
			tw.dropping, tw.heldKeys = map[string]bool{}, map[string]bool{}
			tw.drain(t, ruleNeeds, halvings, 400)
			if len(tw.agenda) != 0 || len(tw.missing) != 0 || tw.openMissing() != 0 {
				t.Fatalf("agenda %v missing %v open missing %d", tw.agenda, tw.missing, tw.openMissing())
			}
			for n, ws := range tw.wait {
				for w := range ws {
					if p := tw.work[n].col; p == Landed || p == "" {
						t.Fatalf("%s still waits for %s, which is %q", w, n, p)
					}
				}
			}
			for _, k := range initial {
				if q := tw.plan(t, ruleNeeds, halvings, k); !posQuiet(q) {
					t.Fatalf("%s again: %+v", k.Key, q)
				}
			}
		})
	}
}

// R15 and R19, the cases the tests did not reach.

// A sprint whose cards were all dropped has no card landed and is done: the
// judgment is raised with the dropped count (the raise needs a card landed or
// dropped, not a card landed).
func TestDoneRaisedForASprintWhoseCardsWereAllDropped(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2")
	tw.ctl("s1", "dropped", "2")
	tw.ctl("s2", "dropped", "1")
	k := posKeyOf("done")
	p, out := tw.run(t, ruleDone, 0, k)
	if out.refused != "" || len(p.Notes) != 1 || p.Notes[0].Op != posOpen || p.Notes[0].Type != NSprintDone || p.Notes[0].Text != "0 landed, 3 dropped" {
		t.Fatalf("plan: %+v %+v", p.Notes, out)
	}
	if !tw.opened(NSprintDone, "", SprintSubject) {
		t.Fatal("the judgment is not open")
	}
	if q := tw.plan(t, ruleDone, 0, k); !posQuiet(q) {
		t.Fatalf("a second plan: %+v", q)
	}
}

// A held "the sprint is done" is still on the sprint: not raised again, and
// closed when work is added.
func TestDoneHeldJudgmentIsNotRaisedAgainAndClosesWhenWorkIsAdded(t *testing.T) {
	t.Parallel()
	tw := posDoneTwin(2, false)
	tw.hold(NSprintDone, "", SprintSubject)
	k := posKeyOf("done")
	if p := tw.plan(t, ruleDone, 0, k); len(p.Notes) != 0 || len(p.Guards) != 0 {
		t.Fatalf("a held judgment was raised again: %+v", p.Notes)
	}
	tw.card("late", "s0", Waiting, 9)
	p, out := tw.run(t, ruleDone, 0, k)
	if out.refused != "" || len(p.Notes) != 1 || p.Notes[0].Op != posClose || len(tw.held) != 0 {
		t.Fatalf("work was added: %+v %+v held %v", p.Notes, out, tw.held)
	}
}

// A pull back that reached the end of the head is done, not requeued: a key
// requeued when nothing was cut would be planned again for nothing.
func TestPullBackNotCutIsDone(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1")
	tw.sentinel("g", "s1", 1)
	for i := 0; i < 10; i++ {
		tw.card("r"+strconv.Itoa(i), "s1", Ready, float64(2+i))
	}
	k := posKeyOf("pullback:s1")
	p, out := tw.run(t, posPullbackRule, 0, k)
	if out.refused != "" || !posHas(p.Done, k.Key) || len(p.Requeue) != 0 || len(p.HeldBack) != 0 {
		t.Fatalf("done %v requeue %v heldback %v %+v", p.Done, p.Requeue, p.HeldBack, out)
	}
	if got := tw.count("s1", Waiting); got != 11 {
		t.Fatalf("waiting %d", got)
	}
}

// The five rules in the order of the tick, each on its own key, on one state,
// and again: every second plan is empty, and after the pull back R3 releases
// nothing behind σ.
func TestPositionRulesInOrderTwiceSecondEmpty(t *testing.T) {
	t.Parallel()
	tw := newPosTwin("s1", "s2", "s3")
	tw.card("a", "s1", Waiting, 1)
	tw.sentinel("g", "s1", 5)
	tw.card("late", "s1", Waiting, 9)
	tw.card("r1", "s1", Ready, 6)
	tw.card("r2", "s1", Ready, 7)
	tw.card("n", "s2", Landed, 0)
	tw.card("w", "s1", Waiting, 11, "open", "1", "needs", "n")
	tw.waiter("n", "w")
	tw.ctl("s3", "state", StreamStopped, "cause", "cross", "other", "n", "card", "m1")
	tw.stuck("s3", "m1", 1)
	tw.ctl("s2")
	keys := map[string]AgendaKey{ruleNeeds: posKeyOf("needs:n"), ruleResolve: posKeyOf("resolve:s1"), ruleCross: posKeyOf("cross"),
		ruleDone: posKeyOf("done"), posPullbackRule: posKeyOf("pullback:s1")}
	var order []string
	for _, r := range RuleTable() {
		if _, ok := keys[r.Name]; ok {
			order = append(order, r.Name)
		}
	}
	if len(order) != 5 {
		t.Fatalf("the rules of the tick: %v", order)
	}
	for round := 1; round <= 2; round++ {
		for _, name := range order {
			p, out := tw.run(t, name, 0, keys[name])
			if out.refused != "" {
				t.Fatalf("round %d, %s: %+v", round, name, out)
			}
			if round == 2 && (out.wrote || !posQuiet(p)) {
				t.Fatalf("round 2, %s is not empty: %+v %+v", name, out, p)
			}
		}
		if why := tw.posPositionHolds("s1"); why != "" {
			t.Fatalf("round %d: %s", round, why)
		}
	}
	if tw.work["r1"].col != Waiting || tw.work["r2"].col != Waiting || tw.work["a"].col != Ready || tw.work["w"].f["open"] != "0" ||
		tw.merge["m1"].col != Queued {
		t.Fatalf("the rules did not do their work: r1 %s a %s open %s m1 %s", tw.work["r1"].col, tw.work["a"].col, tw.work["w"].f["open"], tw.merge["m1"].col)
	}
}

// The reads at the size of a tick's agenda: 2,000 keys of a rule are cut to what
// a read holds, in Go time.

func benchRead(b *testing.B, rule string, keys []AgendaKey) {
	b.Helper()
	var r Rule
	for _, x := range RuleTable() {
		if x.Name == rule {
			r = x
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rp, _ := r.Read(keys, posBounds, 0); len(rp.Sprint) == 0 {
			b.Fatal("the read asks nothing")
		}
	}
}

func BenchmarkReadResolve2000Keys(b *testing.B) {
	keys := make([]AgendaKey, 2000)
	for i := range keys {
		keys[i] = AgendaKey{Key: "resolve:s" + strconv.Itoa(i), Seq: uint64(i + 1)}
	}
	benchRead(b, ruleResolve, keys)
}

func BenchmarkReadNeeds5000Keys(b *testing.B) {
	keys := make([]AgendaKey, 5000)
	for i := range keys {
		keys[i] = AgendaKey{Key: "needs:n" + strconv.Itoa(i), Seq: uint64(i + 1)}
	}
	benchRead(b, ruleNeeds, keys)
}
