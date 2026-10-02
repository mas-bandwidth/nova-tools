package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
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
	require.Equal(t, 0, ready, "after one tick: ready %d, working %d, want 0 and 150", ready, working)
	require.Equal(t, 150, working, "after one tick: ready %d, working %d, want 0 and 150", ready, working)
	for _, m := range widthMembers {
		n := heldBy(s, m)
		require.True(t, n == 18 || n == 19, "%s holds %d (dealt %d this tick), want 18 or 19 round the fleet: %v", m, n, by[m], by)
		require.Equal(t, n, by[m], "%s holds %d (dealt %d this tick), want 18 or 19 round the fleet: %v", m, n, by[m], by)
		require.Equal(t, 64, s.Width(m), "%s width %d", m, s.Width(m))
	}
	require.Equal(t, 0, res.Due, "due %d after a deal the fleet had room for", res.Due)
	h.clean("one tick")
}

// A deal of 500 in one tick (8 x 64 = 512 of room) applies whole: the store cuts
// it into parts under the table layer's entry bounds, and nothing is refused.
func TestOneTickDealsFiveHundred(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 64, 167) // 501 ready, the room 512
	res := h.machine()
	s := h.snap()
	r, w := len(s.Work.Column(sprint.Ready)), len(s.Work.Column(sprint.Working))
	require.Equal(t, 0, r, "ready %d, working %d, want 0 and 501", r, w)
	require.Equal(t, 501, w, "ready %d, working %d, want 0 and 501", r, w)
	for _, p := range res.Parts {
		require.Empty(t, p.Refused, "part %s refused %v", p.Name, p.Refused)
	}
	h.clean("five hundred")
}

// A machine at DealAhead times its width takes no more: after the fleet is full
// the next tick deals nothing, and when a machine finishes work the fleet is
// dealt exactly what was finished, every machine back at DealAhead times its
// width (the level at the tick's start gives the machine that finished a
// share of the others' ready cards; the deal fills the rest).
func TestAMachineAtDealAheadTimesItsWidthTakesNoMore(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 4, 50) // room 8 x 8 = 64 of 150
	full := sprint.DealAhead * 4
	h.machine()
	s := h.snap()
	for _, m := range widthMembers {
		n := heldBy(s, m)
		require.Equal(t, full, n, "%s holds %d, want DealAhead times its width, %d", m, n, full)
	}
	h.tick(time.Second)
	res := h.machine()
	require.Empty(t, dealtBy(res), "a full fleet was dealt %v", dealtBy(res))
	h.work("m3") // takes its eight and finishes them
	h.tick(time.Second)
	by := dealtBy(h.machine())
	total := 0
	for _, n := range by {
		total += n
	}
	require.Equal(t, full, total, "after m3 finished %d: dealt %v, want %d in all", full, by, full)
	for _, m := range widthMembers {
		n := heldBy(h.snap(), m)
		require.Equal(t, full, n, "%s holds %d, want DealAhead times its width, %d", m, n, full)
	}
}

// At width 2 the store's tick deals DealAhead times two cards a machine:
// thirty-two dealt over eight.
func TestWidthTwoDealsDealAheadTimesTwo(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 2, 50)
	h.machine()
	s := h.snap()
	for _, m := range widthMembers {
		n := s.Fleet.Count(m, sprint.Ready)
		require.Equal(t, sprint.DealAhead*2, n, "%s ready %d, want DealAhead times 2", m, n)
	}
	w := len(s.Work.Column(sprint.Working))
	require.Equal(t, 8*sprint.DealAhead*2, w, "working %d, want %d", w, 8*sprint.DealAhead*2)
}

// The fleet table's width column shows each machine's width.
func TestTheFleetTableShowsTheWidth(t *testing.T) {
	t.Parallel()
	h := widthSprint(t, 64, 1)
	h.must(FleetStep(sprint.FleetReq{Op: "release", Member: "m2", Width: 8}))
	shapes, err := h.m.Shapes(h.ctx, []string{"t-fleet"})
	require.NoError(t, err, "shapes: %v", err)
	require.Len(t, shapes, 1, "shapes: %v", err)
	got := map[string]string{}
	for _, r := range shapes[0].Rows {
		got[r.Key] = r.Texts[sprint.FieldWidth]
	}
	require.Equal(t, "64", got["m1"], "the width column: %v", got)
	require.Equal(t, "8", got["m2"], "the width column: %v", got)
	cols := fmt.Sprint(shapes[0].Columns)
	require.Contains(t, cols, "working", "columns %s", cols)
	require.Contains(t, cols, "width", "columns %s", cols)
}
