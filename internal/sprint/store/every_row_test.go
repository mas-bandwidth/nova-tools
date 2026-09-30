package store

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Every row of every table moves every tick, never a row at a time (the
// owner's rule, errata 3 amendment 10): the presence part takes every member
// whose beat lapsed down in the one tick and deals all their cards round the
// members up; the level part evens every queue in the one tick; and the tick
// names every table with the rows it changed.

// fleetOf is n members up at the width, beating, and the store's harness.
func fleetOf(t *testing.T, n, width int) (*harness, []string) {
	t.Helper()
	h := newHarness(t)
	var ms []string
	for i := 1; i <= n; i++ {
		ms = append(ms, "m"+strconv.Itoa(i))
	}
	h.setLive(ms...)
	h.beat()
	for _, m := range ms {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: width}))
	}
	return h, ms
}

// tableRows is the rows a tick names for a table.
func tableRows(res TickResult, table string) []string {
	for _, tb := range res.Tables {
		if tb.Table == table {
			return tb.Rows
		}
	}
	return nil
}

func TestEveryMemberWhoseBeatLapsedGoesDownInOneTick(t *testing.T) {
	t.Parallel()
	h, ms := fleetOf(t, 4, 8)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 32}))
	h.startMachine()
	h.machine()
	if d := h.dealtTo(); d["m1"] != 8 || d["m2"] != 8 || d["m3"] != 8 || d["m4"] != 8 {
		t.Fatalf("dealt %v, want eight each", d)
	}
	// m1 and m2 fall silent together; m3 and m4 have room for none of their
	// cards but take them all, past their width, as a down's cards go
	h.setLive("m3", "m4")
	h.tick(sprint.BeatDeadline + time.Second)
	res := h.machine()
	s := h.snap()
	for _, m := range []string{"m1", "m2"} {
		if st := s.MemberCtl(m).F("status"); st != sprint.Down {
			t.Fatalf("%s is %s after one tick, want down with the other", m, st)
		}
		if n := h.memberNotes(sprint.NMemberDown, m); len(n) != 1 {
			t.Fatalf("%s's down notes: %q", m, n)
		}
	}
	if d := h.dealtTo(); d["m1"] != 0 || d["m2"] != 0 || d["m3"]+d["m4"] != 32 || d["m3"]-d["m4"] > 1 || d["m4"]-d["m3"] > 1 {
		t.Fatalf("after one tick: dealt %v, want every card of m1 and m2 on m3 and m4, evenly", d)
	}
	if got := tableRows(res, sprint.Fleet); !slices.Equal(got, ms) {
		t.Fatalf("the tick names the fleet rows %v, want every member's", got)
	}
	h.clean("two members down in one tick")
}

// A member coming up and one going down in the same tick: both in the one
// presence plan, the down's cards dealt to the members up after it, the
// newcomer's included.
func TestAMemberUpAndAMemberDownInTheSameTick(t *testing.T) {
	t.Parallel()
	h, _ := fleetOf(t, 3, 8)
	h.setLive("m1", "m2")
	h.tick(sprint.BeatDeadline + time.Second)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 12}))
	h.startMachine()
	h.machine() // m3 down, the deal over m1 and m2
	if st := h.snap().MemberCtl("m3").F("status"); st != sprint.Down {
		t.Fatalf("m3 is %s", st)
	}
	h.setLive("m1", "m3")
	h.tick(sprint.BeatDeadline + time.Second)
	h.machine()
	s := h.snap()
	if s.MemberCtl("m2").F("status") != sprint.Down || s.MemberCtl("m3").F("status") != sprint.Up {
		t.Fatalf("after one tick: m2 %s, m3 %s; want m2 down and m3 up", s.MemberCtl("m2").F("status"), s.MemberCtl("m3").F("status"))
	}
	if d := h.dealtTo(); d["m2"] != 0 || d["m1"]+d["m3"] != 12 || d["m3"] == 0 {
		t.Fatalf("dealt %v, want m2's cards on m1 and m3", d)
	}
	h.clean("an up and a down in one tick")
}

// The level part evens every member's queue in the one tick: a member that
// comes back to a fleet whose queues are full takes its share at once.
func TestTheLevelEvensEveryQueueInOneTick(t *testing.T) {
	t.Parallel()
	h, ms := fleetOf(t, 8, 64)
	h.setLive("m1")
	h.tick(sprint.BeatDeadline + time.Second)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 64}))
	h.startMachine()
	h.machine()
	if d := h.dealtTo(); d["m1"] != 64 {
		t.Fatalf("dealt %v, want every card on m1", d)
	}
	h.setLive(ms...)
	h.tick(time.Second)
	h.machine()
	d := h.dealtTo()
	for _, m := range ms {
		if d[m] != 8 {
			t.Fatalf("after one tick with the fleet back: dealt %v, want eight each", d)
		}
	}
	h.clean("the level in one tick")
}

// The work table's stream index survives a stop and a start of the machine:
// the deal after the start goes on past the stream the last deal served.
func TestTheStreamIndexIsReadBackAfterAStop(t *testing.T) {
	t.Parallel()
	h, _ := fleetOf(t, 1, 1)
	for _, st := range []string{"s1", "s2", "s3"} {
		h.must(AddStep(sprint.AddReq{Stream: st, Count: 3}))
	}
	h.startMachine()
	var served []string
	for i := 0; i < 5; i++ {
		res := h.machine()
		idx, _ := h.snap().Work.Prop(sprint.PropStreamIndex)
		served = append(served, idx)
		// the member finishes its one card, so the next tick deals one more
		s := h.snap()
		for _, c := range s.Fleet.Column(sprint.Ready) {
			h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
			h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
		}
		if i == 2 {
			h.stopMachine()
			if idx2, _ := h.snap().Work.Prop(sprint.PropStreamIndex); idx2 != idx {
				t.Fatalf("the stop moved the index from %q to %q", idx, idx2)
			}
			h.startMachine()
		}
		_ = res
	}
	if want := []string{"s1", "s2", "s3", "s1", "s2"}; !slices.Equal(served, want) {
		t.Fatalf("the streams served one a tick, across a stop: %v, want %v", served, want)
	}
}
