package sprint

import (
	"fmt"
	"slices"
	"testing"
)

// Every placement of a card on a member goes round the fleet (errata 3,
// amendment 5: every placement, first attempts and redeals and levelling alike,
// moves the index; round.go): the rework of failed work and of work a read
// found broken (the coordinator's rework), the cards of a member that goes
// down (fleet down and the tick's presence), and the levelling (fleet level
// and the tick's level). The shortest queue with its ties broken by name,
// which this replaces, gave the redeals of an idle fleet to its first members.

// reworkIt sends one primary back with a fix, by the engine under test.
type reworkIt func(w *world, id string)

// stepRework is the coordinator's rework (steps_review.go Rework).
func stepRework(w *world, id string) {
	w.t.Helper()
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{id}}, Fix: "make the test pass"}))
}

// failedOnce is 8 idle members and 30 primaries whose first attempts were
// dealt one at a time and came back failed (broken false) or were found
// broken by a read (broken true): the member each first attempt was on.
func failedOnce(t *testing.T, broken bool) (*world, map[string]string) {
	t.Helper()
	w := eightIdle(t, "reader-a", "reader-b")
	first := map[string]string{}
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		first[id] = wc.Row
		w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		if !broken {
			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Failed: true, Report: "tests red"}))
			continue
		}
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		// a reader found it broken: its read card at the attempt, named on the
		// primary (rcards), as the ask and the read leave it
		putRead(w, id, 1, "reader-a", Broken).Fields["finding"] = "the test is red"
	}
	return w, first
}

// reworksGoRound reworks the 30 one at a time, each worked at once so every
// member is idle again: the second attempts go round the fleet, every member
// within one of the others, none to the member its first attempt was on. The
// first rework is of s1-7, whose first attempt was on m7, the next member round
// the fleet (the index is past m6 after 30 deals): m7 is skipped and it goes to
// m8.
func reworksGoRound(t *testing.T, w *world, first map[string]string, rework reworkIt) {
	t.Helper()
	members := w.s.Fleet.Rows()
	firstN := map[string]int{}
	for _, m := range first {
		firstN[m]++
	}
	evenly(t, "first attempts", firstN, members, false)
	if last, _ := w.s.Fleet.Prop(PropDealIndex); indexPast(members, last) != "m6" || first["s1-7"] != "m7" {
		t.Fatalf("the index is past %q and s1-7 was on %s: want m6 and m7", last, first["s1-7"])
	}
	order := []string{"s1-7"}
	for i := 1; i <= 30; i++ {
		if id := fmt.Sprintf("s1-%d", i); id != "s1-7" {
			order = append(order, id)
		}
	}
	again := map[string]int{}
	var got []string
	for k, id := range order {
		rework(w, id)
		wc := w.s.Fleet.Card(WorkCardID(id, 2))
		if wc == nil || wc.Col != Ready {
			t.Fatalf("rework %d of %s: its second attempt is %+v, want dealt", k+1, id, wc)
		}
		if wc.Row == first[id] {
			t.Fatalf("rework %d: %s went back to %s, the member its first attempt was on", k+1, id, wc.Row)
		}
		if last, _ := w.s.Fleet.Prop(PropDealIndex); indexPast(members, last) != wc.Row {
			t.Fatalf("rework %d: the index is past %q, want past %s, the member dealt to", k+1, last, wc.Row)
		}
		got = append(got, wc.Row)
		again[wc.Row]++
		workIt(w, wc)
	}
	if got[0] != "m8" {
		t.Fatalf("s1-7 went to %s, want m8: m7 is the member it failed on and is skipped", got[0])
	}
	t.Logf("second attempts: %v", again)
	evenly(t, "second attempts", again, members, false)
}

func TestTheReworksOfFailedWorkGoRoundTheFleet(t *testing.T) {
	t.Parallel()
	w, first := failedOnce(t, false)
	reworksGoRound(t, w, first, stepRework)
}

func TestTheReworksOfBrokenReadsGoRoundTheFleet(t *testing.T) {
	t.Parallel()
	w, first := failedOnce(t, true)
	reworksGoRound(t, w, first, stepRework)
}

// The avoid member takes the rework only when no other up member has room.
func TestTheReworkAvoidsTheMemberThatFailedItWhileAnotherHasRoom(t *testing.T) {
	t.Parallel()
	for _, rework := range []reworkIt{stepRework} {
		w := newWorld(t, "reader-a")
		for _, m := range []string{"m1", "m2"} { // each at width 2 (width.go)
			w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: 2}))
		}
		w.must(Add(w.s, AddReq{Stream: "s1", Count: 3}))
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})) // m1
		w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "red"}))
		// m2 at its width: two ready cards, dealt past the index
		w.s.Fleet.SetProps(map[string]string{PropDealIndex: "m1"})
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}}))
		w.s.Fleet.SetProps(map[string]string{PropDealIndex: "m1"})
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-3"}}}))
		if w.s.Fleet.Count("m2", Ready) != 2 {
			t.Fatalf("m2 has %d ready, want 2", w.s.Fleet.Count("m2", Ready))
		}
		rework(w, "s1-1")
		if wc := w.s.Fleet.Card("s1-1.w2"); wc == nil || wc.Row != "m1" {
			t.Fatalf("s1-1's second attempt is %+v, want on m1: no other member has room", wc)
		}
	}
}

// A member that goes down has its cards dealt again round the fleet from the
// index, each to the next member with room, not from the first member in
// name order: m1's six cards, the index past m4, go m5 m6 m7 m8 m2 m3.
func TestADownMembersCardsGoRoundTheFleet(t *testing.T) {
	t.Parallel()
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	want := []string{"m5", "m6", "m7", "m8", "m2", "m3"}
	check := func(t *testing.T, s *Snapshot, what string) {
		t.Helper()
		var got []string
		for i := 1; i <= 6; i++ {
			got = append(got, s.Fleet.Card(WorkCardID(fmt.Sprintf("s1-%d", i), 1)).Row)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: m1's cards went to %v, want %v: round the fleet from past m4", what, got, want)
		}
		if last, _ := s.Fleet.Prop(PropDealIndex); indexPast(s.Fleet.Rows(), last) != "m3" {
			t.Fatalf("%s: the index is past %q, want m3", what, last)
		}
	}

	// fleet down (the verb, and presence's down)
	w := newWorld(t, "reader-a")
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 6}))
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("s1-%d", i)
		w.s.Fleet.SetProps(map[string]string{PropDealIndex: "m8"})
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		if i <= 4 {
			w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{id + ".w1"}}, Gens: gensOf(w.s, id+".w1")}))
		}
	}
	w.s.Fleet.SetProps(map[string]string{PropDealIndex: "m4"})
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	check(t, w.s, "fleet down")

	// the tick's presence: m1 has no beat, the others beat now
	w = fleetWorld(t, 0, 0, members...)
	for i := 1; i <= 6; i++ {
		col := Working
		if i > 4 {
			col = Ready
		}
		putWorkCard(w, fmt.Sprintf("s1-%d", i), "m1", col, float64(10+i), nil)
	}
	w.s.Fleet.SetProps(map[string]string{PropDealIndex: "m4"})
	beats := map[string]Beat{}
	for _, m := range members[1:] {
		beats[m] = Beat{At: w.s.Now}
	}
	w.part(TickPresence, TickReq{Beats: beats})
	check(t, w.s, "presence")
}

// The level goes round the fleet: twelve cards on m1 and seven members idle,
// the index past m5; each card of the longest queue goes to the next member
// below the mean (1), then to the next at it: m6 m7 m8 m2 m3 m4 m5, then m6 m7
// m8, and every queue ends within one of the others.
func TestTheLevelGoesRoundTheFleet(t *testing.T) {
	t.Parallel()
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	want := map[string]int{"m1": 2, "m2": 1, "m3": 1, "m4": 1, "m5": 1, "m6": 2, "m7": 2, "m8": 2}
	check := func(t *testing.T, s *Snapshot, what string) {
		t.Helper()
		got := map[string]int{}
		for _, m := range members {
			got[m] = s.Fleet.Count(m, Ready)
		}
		if !mapsEqual(got, want) {
			t.Fatalf("%s: the queues are %v, want %v: round the fleet from past m5", what, got, want)
		}
		if last, _ := s.Fleet.Prop(PropDealIndex); indexPast(s.Fleet.Rows(), last) != "m8" {
			t.Fatalf("%s: the index is past %q, want m8", what, last)
		}
	}
	upAll := func(s *Snapshot) {
		for _, m := range members[1:] {
			ctl := s.MemberCtl(m)
			ctl.Fields["status"] = Up // up without the step that would level the queues
			s.Fleet.Put(ctl)
		}
		s.Fleet.SetProps(map[string]string{PropDealIndex: "m5"})
	}

	// fleet level (the verb and T4)
	w := newWorld(t, "reader-a")
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	for _, m := range members[1:] {
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: m}))
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 12}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 12}})) // m1 alone: every card spills to it
	upAll(w.s)
	w.must(FleetStep(w.s, FleetReq{Op: "level"}))
	check(t, w.s, "fleet level")

	// the tick's level (T4), on cards put on m1 as a store holds them
	w = fleetWorld(t, 0, 0, members...)
	for i := 1; i <= 12; i++ {
		putWorkCard(w, fmt.Sprintf("s1-%d", i), "m1", Ready, float64(10+i), nil)
	}
	upAll(w.s)
	w.part(TickLevel, TickReq{})
	check(t, w.s, "the tick's level")
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
