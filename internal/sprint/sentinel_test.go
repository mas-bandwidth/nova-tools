package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeOne lands the head of a stream's queue: one merge step, batch 1.
func mergeOne(w *world, stream string) Plan {
	w.t.Helper()
	p := w.must(MergeStep(w.s, MergeReq{Stream: stream, Batch: 1}))
	w.clean("merged in " + stream)
	return p
}

func notesIn(p Plan, typ string) []Note {
	var out []Note
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == typ {
				out = append(out, n)
			}
		}
	}
	return out
}

func release(w *world, reason string, ids ...string) Plan {
	w.t.Helper()
	return Release(w.s, ReleaseReq{IDs: ids, Reason: reason, Coordinator: "coord", Who: "coord"})
}

// landsSentinel says a plan lands a sentinel.
func landsSentinel(s *Snapshot, p Plan) bool {
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table == Work && c.Entry.Move != nil && c.Entry.Move.Col == Landed && IsSentinel(s.Work.Card(c.Entry.ID)) {
				return true
			}
		}
	}
	return false
}

// H7: a stream of five and a sentinel at its end; a card of a second stream
// needs the sentinel. The step that lands the fifth card marks it reached and
// writes one judgment; nothing behind it moves; release lands it and the card
// behind it is ready in the same step, with the landed notification.
func TestASentinelIsAStopTheCoordinatorReleases(t *testing.T) {
	t.Parallel()
	w := setup(t, 5)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true})))
	stop := w.s.Work.Card("stop")
	require.Equal(t, Waiting, stop.Col, "the sentinel: %s %v", stop.Col, stop.Fields)
	require.Empty(t, stop.F("needs"), "the sentinel: %s %v", stop.Col, stop.Fields)
	require.Equal(t, "s1-1,s1-2,s1-3,s1-4,s1-5", strings.Join(WaitsFor(w.s, stop, nil), ","), "the sentinel: %s %v", stop.Col, stop.Fields)
	require.Empty(t, stop.F("reached"), "the sentinel: %s %v", stop.Col, stop.Fields)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"stop"}})))
	w.clean("admitted")
	p := release(w, "looked", "stop")
	require.Empty(t, p.Units, "release before reached: %+v", p)
	require.Len(t, p.Refused, 1, "release before reached: %+v", p)
	require.Equal(t, "not reached: it waits for s1-1 (ready), s1-2 (ready), s1-3 (ready), s1-4 (ready), s1-5 (ready)", p.Refused[0].Why, "release before reached: %+v", p)
	accepted(w, "s1-1", "s1-2", "s1-3", "s1-4", "s1-5")
	for i := 1; i <= 4; i++ {
		p := mergeOne(w, "s1")
		require.Empty(t, notesIn(p, NSentinelReached), "reached early, at s1-%d", i)
		require.False(t, landsSentinel(w.s, p), "reached early, at s1-%d", i)
	}
	p = mergeOne(w, "s1")
	reached := notesIn(p, NSentinelReached)
	if len(reached) != 1 || reached[0].Kind != Judgment || stop.F("reached") == "" || stop.Col != Waiting || landsSentinel(w.s, p) ||
		reached[0].What != "sentinel stop reached: 5 cards of s1 have landed; 1 cards wait behind it" {
		t.Fatalf("the fifth landing: %+v, stop %s %v", reached, stop.Col, stop.Fields)
	}
	require.Equal(t, Waiting, w.state("b"), "b is %s; reached notes %d", w.state("b"), len(w.notesOf(NSentinelReached)))
	require.Len(t, w.notesOf(NSentinelReached), 1, "b is %s; reached notes %d", w.state("b"), len(w.notesOf(NSentinelReached)))
	p = SentinelsDue(w.s, "")
	require.Empty(t, p.Units, "reached twice: %+v", p)
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open})
	require.Equal(t, NSentinelReached, g[0].Type, "the inbox: %+v", g[0])
	require.Equal(t, "nova-sprint release stop --reason '<what you looked at and found>' --answers "+g[0].ID, g[0].Commands[0].Lines[0], "the inbox: %+v", g[0])
	p = w.must(release(w, "the layer is green and read", "stop"))
	landed := w.notesOf(NSentinelLanded)
	if stop.Col != Landed || stop.F("released_by") != "coord" || stop.F("release_reason") != "the layer is green and read" || w.state("b") != Ready ||
		len(landed) != 1 || landed[0].Kind != Happened || landed[0].What != "sentinel stop landed, released by coord: 1 cards are now ready; the layer is green and read" ||
		len(w.openOn("stop")) != 0 {
		t.Fatalf("release: stop %s, b %s, %+v", stop.Col, w.state("b"), landed)
	}
	require.Equal(t, StreamLanded, w.s.StreamCtl("s1").F("state"), "stream s1 is %s", w.s.StreamCtl("s1").F("state"))
	w.clean("released")
}

// H7: the coordinator alone releases, with a reason; no other step lands a
// sentinel, and a plan that tries is refused by the lifecycle.
func TestOnlyTheCoordinatorReleases(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true})))
	accepted(w, "s1-1")
	landing := mergeOne(w, "s1")
	require.False(t, landsSentinel(w.s, landing), "the landing trigger: %+v", landing)
	require.NotEmpty(t, w.s.Work.Card("stop").F("reached"), "the landing trigger: %+v", landing)
	for _, r := range []ReleaseReq{
		{IDs: []string{"stop"}, Reason: "x", Coordinator: "coord", Who: "someone"},
		{IDs: []string{"stop"}, Coordinator: "coord", Who: "coord"},
	} {
		p := Release(w.s, r)
		require.Empty(t, p.Units, "%+v: %+v", r, p)
		require.Len(t, p.Refused, 1, "%+v: %+v", r, p)
	}
	another := Release(w.s, ReleaseReq{IDs: []string{"stop"}, Reason: "x", Coordinator: "coord", Who: "someone"})
	require.Equal(t, "release is the coordinator's alone: coord, not someone", another.Refused[0].Why, "another actor: %+v", another.Refused)
	stop := w.s.Work.Card("stop")
	for _, name := range []string{"resolve", "sentinels due", "a raw plan"} {
		var p Plan
		switch name {
		case "resolve":
			p = Resolve(w.s, ResolveReq{Sel: Sel{IDs: []string{"stop"}}})
		case "sentinels due":
			p = SentinelsDue(w.s, "")
		case "a raw plan":
			p.on(w.s)
			p.Units = []Unit{{Key: "stop", Stream: "s1", Changes: []Change{change(Work, moveEntry(stop, "s1", Landed, nil))}}}
			p = Lawful(p)
			require.Len(t, p.Refused, 1, "a raw landing: %+v", p)
			require.Equal(t, "the lifecycle lands from waiting only a sentinel, and only by release", p.Refused[0].Why, "a raw landing: %+v", p)
		}
		require.False(t, landsSentinel(w.s, p), "%s lands a sentinel: %+v", name, p)
	}
	p := Lawful(Plan{Units: []Unit{{Key: "stop", Changes: []Change{change(Work, moveEntry(stop, "s1", Ready, nil))}}}})
	require.Empty(t, p.Units, "a sentinel moved to ready: %+v", p)
}

// H7: a chain of three sentinels: releasing one makes the next reached, never
// landed, in the same step.
func TestAChainOfSentinelsIsReleasedOneAtATime(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	for _, id := range []string{"x1", "x2", "x3"} {
		w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{id}, Sentinel: true})))
	}
	x2, x3 := strings.Join(WaitsFor(w.s, w.s.Work.Card("x2"), nil), ","), strings.Join(WaitsFor(w.s, w.s.Work.Card("x3"), nil), ",")
	require.Equal(t, "s1-1,x1", x2, "waits by position: x2 %q x3 %q", x2, x3)
	require.Equal(t, "s1-1,x1,x2", x3, "waits by position: x2 %q x3 %q", x2, x3)
	require.Empty(t, w.s.Work.Card("x3").F("needs"), "waits by position: x2 %q x3 %q", x2, x3)
	accepted(w, "s1-1")
	mergeOne(w, "s1")
	for i, id := range []string{"x1", "x2", "x3"} {
		c := w.s.Work.Card(id)
		require.NotEmpty(t, c.F("reached"), "%s not reached", id)
		require.Len(t, w.notesOf(NSentinelReached), i+1, "%s not reached", id)
		p := w.must(release(w, "go on", id))
		require.Equal(t, Landed, c.Col, "%s is %s", id, c.Col)
		if i < 2 {
			next := w.s.Work.Card([]string{"x2", "x3"}[i])
			require.Equal(t, Waiting, next.Col, "releasing %s: the next is %s reached %q", id, next.Col, next.F("reached"))
			require.NotEmpty(t, next.F("reached"), "releasing %s: the next is %s reached %q", id, next.Col, next.F("reached"))
			require.Len(t, notesIn(p, NSentinelReached), 1, "releasing %s: the next is %s reached %q", id, next.Col, next.F("reached"))
		}
		w.clean("released " + id)
	}
}

// H7: a card before a sentinel that is dropped is no longer before it: the
// stop is reached with no judgment. A need the sentinel names that is
// dropped blocks it like any waiting card; ack waives it, and it is reached.
func TestADroppedNeedOfASentinel(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"x"}}))
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Needs: []string{"x"}})))
	accepted(w, "s1-1")
	mergeOne(w, "s1")
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	open := w.openOn("stop")
	require.Empty(t, open, "a card before the stop dropped: open %+v, waits %v", open, WaitsFor(w.s, w.s.Work.Card("stop"), nil))
	require.Equal(t, "x", strings.Join(WaitsFor(w.s, w.s.Work.Card("stop"), nil), ","), "a card before the stop dropped: open %+v, waits %v", open, WaitsFor(w.s, w.s.Work.Card("stop"), nil))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"x"}}, Reason: "obsolete"}))
	blocked := w.openOn("stop")
	require.Len(t, blocked, 1, "blocked: %+v", blocked)
	require.Equal(t, NBlocked, blocked[0].Note.Type, "blocked: %+v", blocked)
	require.Empty(t, w.s.Work.Card("stop").F("reached"), "blocked: %+v", blocked)
	p := w.must(Ack(w.s, AckReq{Notes: []string{blocked[0].Note.ID}, Reason: "not needed", Who: "coordinator"}))
	stop := w.s.Work.Card("stop")
	require.Equal(t, "x", stop.F("waived"), "ack: %v", stop.Fields)
	require.NotEmpty(t, stop.F("reached"), "ack: %v", stop.Fields)
	require.Equal(t, Waiting, stop.Col, "ack: %v", stop.Fields)
	require.Len(t, notesIn(p, NSentinelReached), 1, "ack: %v", stop.Fields)
	w.clean("waived")
	tp, _ := TickDone(w.s, TickReq{})
	require.True(t, tp.Empty(), "the sprint is done with a sentinel waiting: %+v", tp)
	w.must(release(w, "done", "stop"))
	w.must(tickDone(w.s, TickReq{}))
	done := w.notesOf(NSprintDone)
	require.Len(t, done, 1, "the sprint is done: %+v", done)
	require.Equal(t, "2 landed, 2 dropped", done[0].What, "the sprint is done: %+v", done)
	w.clean("released")
}

// H7: a sentinel with needs in two streams is reached by the landing of the
// last of them, in either stream.
func TestASentinelWithNeedsInTwoStreams(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Needs: []string{"s2-1"}})))
	got, waits := w.s.Work.Card("stop").F("needs"), strings.Join(WaitsFor(w.s, w.s.Work.Card("stop"), nil), ",")
	require.Equal(t, "s2-1", got, "needs named %q, waits %q", got, waits)
	require.Equal(t, "s2-1,s1-1", waits, "needs named %q, waits %q", got, waits)
	accepted(w, "s1-1")
	mergeOne(w, "s1")
	require.Empty(t, w.s.Work.Card("stop").F("reached"), "reached with s2-1 open")
	accepted(w, "s2-1")
	p := mergeOne(w, "s2")
	require.Len(t, notesIn(p, NSentinelReached), 1, "s2's landing did not reach it: %+v", p.Units)
}

// H7: by position: a card added after a sentinel waits behind it; a card
// added --before it is a need of it, and un-reaches it until it lands.
func TestASentinelStopsTheStreamByPosition(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a1", "a2"}}))
	w.must(Lawful(Add(w.s, AddReq{Stream: "a", IDs: []string{"A"}, Sentinel: true})))
	p := w.must(Lawful(Add(w.s, AddReq{Stream: "a", IDs: []string{"b1"}})))
	b1 := w.s.Work.Card("b1")
	require.Equal(t, Waiting, w.state("b1"), "b1: %s %v %q", w.state("b1"), w.s.Work.Card("b1").Fields, p.Units[0].Moved)
	require.Empty(t, b1.F("needs"), "b1: %s %v %q", w.state("b1"), w.s.Work.Card("b1").Fields, p.Units[0].Moved)
	require.Equal(t, "A", strings.Join(WaitsFor(w.s, b1, nil), ","), "b1: %s %v %q", w.state("b1"), w.s.Work.Card("b1").Fields, p.Units[0].Moved)
	require.Contains(t, p.Units[0].Moved, "waits behind sentinel A", "b1: %s %v %q", w.state("b1"), w.s.Work.Card("b1").Fields, p.Units[0].Moved)
	accepted(w, "a1", "a2")
	mergeOne(w, "a")
	mergeOne(w, "a")
	A := w.s.Work.Card("A")
	require.NotEmpty(t, A.F("reached"), "A not reached")
	w.must(Lawful(Add(w.s, AddReq{Stream: "a", IDs: []string{"a3"}, Before: "A"})))
	if A.F("reached") != "" || !contains(WaitsFor(w.s, A, nil), "a3") || A.F("needs") != "" || len(w.openOn("A")) != 0 || w.state("a3") != Ready || w.state("b1") != Waiting {
		t.Fatalf("a3 before A: A %v, open %v, a3 %s, b1 %s", A.Fields, w.openOn("A"), w.state("a3"), w.state("b1"))
	}
	a3 := w.s.Work.Card("a3")
	require.Less(t, a3.Score, A.Score, "a3 is not in front of A: %v < %v, needs %q", a3.Score, A.Score, a3.F("needs"))
	require.Empty(t, a3.F("needs"), "a3 is not in front of A: %v < %v, needs %q", a3.Score, A.Score, a3.F("needs"))
	w.clean("a3 in front")
	accepted(w, "a3")
	judgments := 0
	p = mergeOne(w, "a")
	for _, n := range w.notesOf(NSentinelReached) {
		if n.Kind == Judgment {
			judgments++
		}
	}
	require.Len(t, notesIn(p, NSentinelReached), 1, "A reached again: %+v", w.notesOf(NSentinelReached))
	require.Equal(t, 2, judgments, "A reached again: %+v", w.notesOf(NSentinelReached))
	w.must(release(w, "layer a is good", "A"))
	require.Equal(t, Ready, w.state("b1"), "b1 is %s", w.state("b1"))
	w.clean("released")
}

// H7: a sentinel inserted in line: the ready cards behind it go back to
// waiting, the card in flight is listed as past the stop and waited for, and
// nothing behind it is dealt until it is released.
func TestASentinelInsertedInLine(t *testing.T) {
	t.Parallel()
	w := setup(t, 6)
	accepted(w, "s1-1", "s1-2", "s1-3")
	for i := 0; i < 3; i++ {
		mergeOne(w, "s1")
	}
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-4"}}}))
	p := w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, After: "s1-4"})))
	stop := w.s.Work.Card("stop")
	if w.state("s1-5") != Waiting || w.state("s1-6") != Waiting || stop.F("needs") != "" || strings.Join(WaitsFor(w.s, stop, nil), ",") != "s1-4" ||
		!strings.Contains(p.Units[0].Moved, "already past the stop: s1-4") || !strings.Contains(p.Units[0].Moved, "s1-5,s1-6 ready -> waiting behind it") {
		t.Fatalf("inserted: s1-5 %s s1-6 %s waits %v moved %q", w.state("s1-5"), w.state("s1-6"), WaitsFor(w.s, stop, nil), p.Units[0].Moved)
	}
	require.Less(t, w.s.Work.Card("s1-4").Score, stop.Score, "the stop is not in line after s1-4")
	require.Less(t, stop.Score, w.s.Work.Card("s1-5").Score, "the stop is not in line after s1-4")
	w.clean("inserted")
	p = Deal(w.s, DealReq{Sel: Sel{Limit: 10}})
	require.Empty(t, p.Units, "dealt past the stop: %+v", p.Units)
	c := w.s.Fleet.Card("s1-4.w1")
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Ask(w.s, AskReq{}))
	for _, rc := range readsAt(w.s, w.s.Work.Card("s1-4"), 1) {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-4"}}}))
	p = mergeOne(w, "s1")
	require.Len(t, notesIn(p, NSentinelReached), 1, "not reached when s1-4 landed")
	w.must(release(w, "checked", "stop"))
	require.Equal(t, Ready, w.state("s1-5"), "after release: %s %s", w.state("s1-5"), w.state("s1-6"))
	require.Equal(t, Ready, w.state("s1-6"), "after release: %s %s", w.state("s1-5"), w.state("s1-6"))
	w.clean("released")
	// No score between two neighbours: refused, not renumbered.
	w.s.Work.Card("s1-6").Score = w.s.Work.Card("s1-5").Score
	w.s.Work.cells = nil
	p = Add(w.s, AddReq{Stream: "s1", IDs: []string{"x"}, Sentinel: true, After: "s1-5"})
	require.Len(t, p.Refused, 1, "no room: %+v", p)
	require.Contains(t, p.Refused[0].Why, "no score lies between", "no room: %+v", p)
}
