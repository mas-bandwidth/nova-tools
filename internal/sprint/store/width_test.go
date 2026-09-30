package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// widthMembers is eight fleet machines (errata 3 amendment 9: "each fleet
// machine to have say, max width 64").
var widthMembers = []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}

// widthSprint is the owner's fleet on the store: eight machines of the width,
// each beating, and three streams of perStream ready primaries, the machine
// running.
func widthSprint(t *testing.T, width, perStream int) *harness {
	t.Helper()
	h := newHarness(t)
	h.mu.Lock()
	h.live = append([]string(nil), widthMembers...)
	h.mu.Unlock()
	h.beat()
	for _, m := range widthMembers {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: width}))
	}
	for _, st := range []string{"s1", "s2", "s3"} {
		h.must(AddStep(sprint.AddReq{Stream: st, Count: perStream}))
	}
	h.startMachine()
	return h
}

// dealtBy is the deal part's moves of one tick by member.
func dealtBy(res TickResult) map[string]int {
	out := map[string]int{}
	for _, p := range res.Parts {
		if p.Name != "deal" {
			continue
		}
		for _, line := range p.Moved {
			if _, m, ok := strings.Cut(line, " member="); ok {
				m, _, _ = strings.Cut(m, " ")
				out[m]++
			}
		}
	}
	return out
}

// One tick of the store's machine: 150 ready over eight machines of width 64
// all go to working, round the fleet, 18 or 19 a machine, in the deal's one
// part.
func TestOneTickDealsTheWholeReadyColumnToWidth(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 64, 50)
	res := h.machine()
	s := h.snap()
	ready, working := len(s.Work.Column(sprint.Ready)), len(s.Work.Column(sprint.Working))
	by := dealtBy(res)
	t.Logf("after one tick: ready %d, working %d; dealt by machine %v", ready, working, by)
	if ready != 0 || working != 150 {
		t.Fatalf("after one tick: ready %d, working %d, want 0 and 150", ready, working)
	}
	for _, m := range widthMembers {
		n := heldBy(s, m)
		if n != 18 && n != 19 || by[m] != n {
			t.Fatalf("%s holds %d (dealt %d this tick), want 18 or 19 round the fleet: %v", m, n, by[m], by)
		}
		if s.Width(m) != 64 {
			t.Fatalf("%s width %d", m, s.Width(m))
		}
	}
	if res.Due != 0 {
		t.Fatalf("due %d after a deal the fleet had room for", res.Due)
	}
	h.clean("one tick")
}

// A deal of 500 in one tick (8 x 64 = 512 of room) applies whole: the store cuts
// it into parts under the table layer's entry bounds, and nothing is refused.
func TestOneTickDealsFiveHundred(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 64, 167) // 501 ready, the room 512
	res := h.machine()
	s := h.snap()
	if r, w := len(s.Work.Column(sprint.Ready)), len(s.Work.Column(sprint.Working)); r != 0 || w != 501 {
		t.Fatalf("ready %d, working %d, want 0 and 501", r, w)
	}
	for _, p := range res.Parts {
		if len(p.Refused) > 0 {
			t.Fatalf("part %s refused %v", p.Name, p.Refused)
		}
	}
	h.clean("five hundred")
}

// A machine at its width takes no more: after the fleet is full the next tick
// deals nothing, and a machine that finishes work takes exactly what it
// finished.
func TestAMachineAtItsWidthTakesNoMore(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 4, 50) // room 32 of 150
	h.machine()
	s := h.snap()
	for _, m := range widthMembers {
		if n := heldBy(s, m); n != 4 {
			t.Fatalf("%s holds %d, want its width 4", m, n)
		}
	}
	h.tick(time.Second)
	if res := h.machine(); len(dealtBy(res)) != 0 {
		t.Fatalf("a full fleet was dealt %v", dealtBy(res))
	}
	h.work("m3") // takes its four and finishes them
	h.tick(time.Second)
	by := dealtBy(h.machine())
	if len(by) != 1 || by["m3"] != 4 {
		t.Fatalf("after m3 finished four: dealt %v, want m3:4", by)
	}
	for _, m := range widthMembers {
		if n := heldBy(h.snap(), m); n > 4 {
			t.Fatalf("%s holds %d past its width", m, n)
		}
	}
}

// At width 2 the store's tick is today's: two cards a machine, sixteen dealt.
func TestWidthTwoIsTodaysDeal(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 2, 50)
	h.machine()
	s := h.snap()
	for _, m := range widthMembers {
		if n := s.Fleet.Count(m, sprint.Ready); n != 2 {
			t.Fatalf("%s ready %d, want 2", m, n)
		}
	}
	if w := len(s.Work.Column(sprint.Working)); w != 16 {
		t.Fatalf("working %d, want 16", w)
	}
}

// The fleet table's width column shows each machine's width.
func TestTheFleetTableShowsTheWidth(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 64, 1)
	h.must(FleetStep(sprint.FleetReq{Op: "release", Member: "m2", Width: 8}))
	shapes, err := h.m.Shapes(h.ctx, []string{"t-fleet"})
	if err != nil || len(shapes) != 1 {
		t.Fatalf("shapes: %v", err)
	}
	got := map[string]string{}
	for _, r := range shapes[0].Rows {
		got[r.Key] = r.Texts[sprint.FieldWidth]
	}
	if got["m1"] != "64" || got["m2"] != "8" {
		t.Fatalf("the width column: %v", got)
	}
	cols := fmt.Sprint(shapes[0].Columns)
	if !strings.Contains(cols, "working") || !strings.Contains(cols, "width") {
		t.Fatalf("columns %s", cols)
	}
}
