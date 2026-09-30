package sprint

import (
	"strings"
	"testing"
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
	if stop.Col != Waiting || stop.F("needs") != "" || strings.Join(WaitsFor(w.s, stop, nil), ",") != "s1-1,s1-2,s1-3,s1-4,s1-5" || stop.F("reached") != "" {
		t.Fatalf("the sentinel: %s %v", stop.Col, stop.Fields)
	}
	w.must(Lawful(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"stop"}})))
	w.clean("admitted")
	p := release(w, "looked", "stop")
	if len(p.Units) != 0 || len(p.Refused) != 1 || p.Refused[0].Why != "not reached: it waits for s1-1 (ready), s1-2 (ready), s1-3 (ready), s1-4 (ready), s1-5 (ready)" {
		t.Fatalf("release before reached: %+v", p)
	}
	accepted(w, "s1-1", "s1-2", "s1-3", "s1-4", "s1-5")
	for i := 1; i <= 4; i++ {
		if p := mergeOne(w, "s1"); len(notesIn(p, NSentinelReached)) != 0 || landsSentinel(w.s, p) {
			t.Fatalf("reached early, at s1-%d", i)
		}
	}
	p = mergeOne(w, "s1")
	reached := notesIn(p, NSentinelReached)
	if len(reached) != 1 || reached[0].Kind != Judgment || stop.F("reached") == "" || stop.Col != Waiting || landsSentinel(w.s, p) ||
		reached[0].What != "sentinel stop reached: 5 cards of s1 have landed; 1 cards wait behind it" {
		t.Fatalf("the fifth landing: %+v, stop %s %v", reached, stop.Col, stop.Fields)
	}
	if w.state("b") != Waiting || len(w.notesOf(NSentinelReached)) != 1 {
		t.Fatalf("b is %s; reached notes %d", w.state("b"), len(w.notesOf(NSentinelReached)))
	}
	if p := SentinelsDue(w.s, ""); len(p.Units) != 0 {
		t.Fatalf("reached twice: %+v", p)
	}
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open})
	if g[0].Type != NSentinelReached || g[0].Commands[0].Lines[0] != "nova-sprint release stop --reason '<what you looked at and found>' --answers "+g[0].ID {
		t.Fatalf("the inbox: %+v", g[0])
	}
	p = w.must(release(w, "the layer is green and read", "stop"))
	landed := w.notesOf(NSentinelLanded)
	if stop.Col != Landed || stop.F("released_by") != "coord" || stop.F("release_reason") != "the layer is green and read" || w.state("b") != Ready ||
		len(landed) != 1 || landed[0].Kind != Happened || landed[0].What != "sentinel stop landed, released by coord: 1 cards are now ready; the layer is green and read" ||
		len(w.openOn("stop")) != 0 {
		t.Fatalf("release: stop %s, b %s, %+v", stop.Col, w.state("b"), landed)
	}
	if w.s.StreamCtl("s1").F("state") != StreamLanded {
		t.Fatalf("stream s1 is %s", w.s.StreamCtl("s1").F("state"))
	}
	w.clean("released")
}

// H7: the coordinator alone releases, with a reason; no other step lands a
// sentinel, and a plan that tries is refused by the lifecycle.
func TestOnlyTheCoordinatorReleases(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true})))
	accepted(w, "s1-1")
	if p := mergeOne(w, "s1"); landsSentinel(w.s, p) || w.s.Work.Card("stop").F("reached") == "" {
		t.Fatalf("the landing trigger: %+v", p)
	}
	for _, r := range []ReleaseReq{
		{IDs: []string{"stop"}, Reason: "x", Coordinator: "coord", Who: "someone"},
		{IDs: []string{"stop"}, Coordinator: "coord", Who: "coord"},
	} {
		if p := Release(w.s, r); len(p.Units) != 0 || len(p.Refused) != 1 {
			t.Fatalf("%+v: %+v", r, p)
		}
	}
	if p := Release(w.s, ReleaseReq{IDs: []string{"stop"}, Reason: "x", Coordinator: "coord", Who: "someone"}); p.Refused[0].Why != "release is the coordinator's alone: coord, not someone" {
		t.Fatalf("another actor: %+v", p.Refused)
	}
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
			if len(p.Refused) != 1 || p.Refused[0].Why != "the lifecycle lands from waiting only a sentinel, and only by release" {
				t.Fatalf("a raw landing: %+v", p)
			}
		}
		if landsSentinel(w.s, p) {
			t.Fatalf("%s lands a sentinel: %+v", name, p)
		}
	}
	if p := Lawful(Plan{Units: []Unit{{Key: "stop", Changes: []Change{change(Work, moveEntry(stop, "s1", Ready, nil))}}}}); len(p.Units) != 0 {
		t.Fatalf("a sentinel moved to ready: %+v", p)
	}
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
	if x2 != "s1-1,x1" || x3 != "s1-1,x1,x2" || w.s.Work.Card("x3").F("needs") != "" {
		t.Fatalf("waits by position: x2 %q x3 %q", x2, x3)
	}
	accepted(w, "s1-1")
	mergeOne(w, "s1")
	for i, id := range []string{"x1", "x2", "x3"} {
		c := w.s.Work.Card(id)
		if c.F("reached") == "" || len(w.notesOf(NSentinelReached)) != i+1 {
			t.Fatalf("%s not reached", id)
		}
		p := w.must(release(w, "go on", id))
		if c.Col != Landed {
			t.Fatalf("%s is %s", id, c.Col)
		}
		if i < 2 {
			next := w.s.Work.Card([]string{"x2", "x3"}[i])
			if next.Col != Waiting || next.F("reached") == "" || len(notesIn(p, NSentinelReached)) != 1 {
				t.Fatalf("releasing %s: the next is %s reached %q", id, next.Col, next.F("reached"))
			}
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
	if open := w.openOn("stop"); len(open) != 0 || strings.Join(WaitsFor(w.s, w.s.Work.Card("stop"), nil), ",") != "x" {
		t.Fatalf("a card before the stop dropped: open %+v, waits %v", open, WaitsFor(w.s, w.s.Work.Card("stop"), nil))
	}
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"x"}}, Reason: "obsolete"}))
	blocked := w.openOn("stop")
	if len(blocked) != 1 || blocked[0].Note.Type != NBlocked || w.s.Work.Card("stop").F("reached") != "" {
		t.Fatalf("blocked: %+v", blocked)
	}
	p := w.must(Ack(w.s, AckReq{Notes: []string{blocked[0].Note.ID}, Reason: "not needed", Who: "coordinator"}))
	stop := w.s.Work.Card("stop")
	if stop.F("waived") != "x" || stop.F("reached") == "" || stop.Col != Waiting || len(notesIn(p, NSentinelReached)) != 1 {
		t.Fatalf("ack: %v", stop.Fields)
	}
	w.clean("waived")
	if p, _ := TickDone(w.s, TickReq{}); !p.Empty() {
		t.Fatalf("the sprint is done with a sentinel waiting: %+v", p)
	}
	w.must(release(w, "done", "stop"))
	w.must(tickDone(w.s, TickReq{}))
	if done := w.notesOf(NSprintDone); len(done) != 1 || done[0].What != "2 landed, 2 dropped" {
		t.Fatalf("the sprint is done: %+v", done)
	}
	w.clean("released")
}

// H7: a sentinel with needs in two streams is reached by the landing of the
// last of them, in either stream.
func TestASentinelWithNeedsInTwoStreams(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	w.must(Lawful(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Needs: []string{"s2-1"}})))
	if got, waits := w.s.Work.Card("stop").F("needs"), strings.Join(WaitsFor(w.s, w.s.Work.Card("stop"), nil), ","); got != "s2-1" || waits != "s2-1,s1-1" {
		t.Fatalf("needs named %q, waits %q", got, waits)
	}
	accepted(w, "s1-1")
	mergeOne(w, "s1")
	if w.s.Work.Card("stop").F("reached") != "" {
		t.Fatalf("reached with s2-1 open")
	}
	accepted(w, "s2-1")
	if p := mergeOne(w, "s2"); len(notesIn(p, NSentinelReached)) != 1 {
		t.Fatalf("s2's landing did not reach it: %+v", p.Units)
	}
}

// H7: by position: a card added after a sentinel waits behind it; a card
// added --before it is a need of it, and un-reaches it until it lands.
func TestASentinelStopsTheStreamByPosition(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a1", "a2"}}))
	w.must(Lawful(Add(w.s, AddReq{Stream: "a", IDs: []string{"A"}, Sentinel: true})))
	p := w.must(Lawful(Add(w.s, AddReq{Stream: "a", IDs: []string{"b1"}})))
	if b1 := w.s.Work.Card("b1"); w.state("b1") != Waiting || b1.F("needs") != "" || strings.Join(WaitsFor(w.s, b1, nil), ",") != "A" || !strings.Contains(p.Units[0].Moved, "waits behind sentinel A") {
		t.Fatalf("b1: %s %v %q", w.state("b1"), w.s.Work.Card("b1").Fields, p.Units[0].Moved)
	}
	accepted(w, "a1", "a2")
	mergeOne(w, "a")
	mergeOne(w, "a")
	A := w.s.Work.Card("A")
	if A.F("reached") == "" {
		t.Fatalf("A not reached")
	}
	w.must(Lawful(Add(w.s, AddReq{Stream: "a", IDs: []string{"a3"}, Before: "A"})))
	if A.F("reached") != "" || !contains(WaitsFor(w.s, A, nil), "a3") || A.F("needs") != "" || len(w.openOn("A")) != 0 || w.state("a3") != Ready || w.state("b1") != Waiting {
		t.Fatalf("a3 before A: A %v, open %v, a3 %s, b1 %s", A.Fields, w.openOn("A"), w.state("a3"), w.state("b1"))
	}
	if a3 := w.s.Work.Card("a3"); !(a3.Score < A.Score) || a3.F("needs") != "" {
		t.Fatalf("a3 is not in front of A: %v < %v, needs %q", a3.Score, A.Score, a3.F("needs"))
	}
	w.clean("a3 in front")
	accepted(w, "a3")
	judgments := 0
	p = mergeOne(w, "a")
	for _, n := range w.notesOf(NSentinelReached) {
		if n.Kind == Judgment {
			judgments++
		}
	}
	if len(notesIn(p, NSentinelReached)) != 1 || judgments != 2 {
		t.Fatalf("A reached again: %+v", w.notesOf(NSentinelReached))
	}
	w.must(release(w, "layer a is good", "A"))
	if w.state("b1") != Ready {
		t.Fatalf("b1 is %s", w.state("b1"))
	}
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
	if !(w.s.Work.Card("s1-4").Score < stop.Score && stop.Score < w.s.Work.Card("s1-5").Score) {
		t.Fatalf("the stop is not in line after s1-4")
	}
	w.clean("inserted")
	if p := Deal(w.s, DealReq{Sel: Sel{Limit: 10}}); len(p.Units) != 0 {
		t.Fatalf("dealt past the stop: %+v", p.Units)
	}
	c := w.s.Fleet.Card("s1-4.w1")
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Ask(w.s, AskReq{}))
	for _, rc := range readsAt(w.s, w.s.Work.Card("s1-4"), 1) {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-4"}}}))
	if p := mergeOne(w, "s1"); len(notesIn(p, NSentinelReached)) != 1 {
		t.Fatalf("not reached when s1-4 landed")
	}
	w.must(release(w, "checked", "stop"))
	if w.state("s1-5") != Ready || w.state("s1-6") != Ready {
		t.Fatalf("after release: %s %s", w.state("s1-5"), w.state("s1-6"))
	}
	w.clean("released")
	// No score between two neighbours: refused, not renumbered.
	w.s.Work.Card("s1-6").Score = w.s.Work.Card("s1-5").Score
	w.s.Work.cells = nil
	if p := Add(w.s, AddReq{Stream: "s1", IDs: []string{"x"}, Sentinel: true, After: "s1-5"}); len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "no score lies between") {
		t.Fatalf("no room: %+v", p)
	}
}
