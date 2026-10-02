package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fleet of the owner's ruling (errata 3 amendment 9): eight machines of
// width 64 and three streams of fifty ready primaries each.
var widthMembers = []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}

func widthFleet(t *testing.T, width int) *world {
	t.Helper()
	w := fleetWorld(t, 50, width, widthMembers...)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 50}))
	w.must(Add(w.s, AddReq{Stream: "s3", Count: 50}))
	return w
}

// perMember is each member's ready and working cards.
func perMember(s *Snapshot) map[string]int {
	out := map[string]int{}
	for _, m := range s.Fleet.Rows() {
		out[m] = s.Fleet.Count(m, Ready) + s.Fleet.Count(m, Working)
	}
	return out
}

// The tick's deal (T3): one plan deals all 150, round the fleet, each machine
// 18 or 19; ready is empty and working holds 150 after the one step.
func TestDealFillsTheFleetToWidthInOnePlan(t *testing.T) {
	t.Parallel()
	w := widthFleet(t, 64)
	p := w.part(TickDeal, TickReq{})
	require.Len(t, p.Units, 150, "one plan dealt %d, want 150", len(p.Units))
	s := w.s
	ready, working := len(s.Work.Column(Ready)), len(s.Work.Column(Working))
	require.Equal(t, 0, ready, "after one plan: ready %d, working %d, want 0 and 150", ready, working)
	require.Equal(t, 150, working, "after one plan: ready %d, working %d, want 0 and 150", ready, working)
	for m, n := range perMember(s) {
		require.Contains(t, []int{18, 19}, n, "%s holds %d, want 18 or 19 (round the fleet): %v", m, n, perMember(s))
	}
}

// A machine at DealAhead times its width takes no more: m1 holding 128 (64
// working, 64 ready) at width 64 is skipped, and the others take the cards.
func TestDealSkipsAMachineAtDealAheadTimesItsWidth(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 10, 64, "m1", "m2")
	for i := range DealAhead * 64 {
		col := Ready
		if i%2 == 0 {
			col = Working
		}
		putWorkCard(w, fmt.Sprintf("x%d", i), "m1", col, float64(100+i), nil)
	}
	p := w.part(TickDeal, TickReq{})
	require.Len(t, p.Units, 10, "dealt %d, want 10", len(p.Units))
	for _, u := range p.Units {
		c := u.Changes[0].Entry.Create
		require.NotNil(t, c, "a card went to a member at DealAhead times its width: %+v", u)
		require.Equal(t, "m2", c.Row, "a card went to a member at DealAhead times its width: %+v", u)
	}
	n := w.s.Fleet.Count("m1", Ready) + w.s.Fleet.Count("m1", Working)
	require.Equal(t, DealAhead*64, n, "m1 holds %d, DealAhead times its width 64 is %d", n, DealAhead*64)
}

// At width 2 the deal gives each member DealAhead times two: thirty-two over
// eight.
func TestDealAtWidthTwoDealsDealAheadTimesTwo(t *testing.T) {
	t.Parallel()
	w := widthFleet(t, 2)
	p := w.part(TickDeal, TickReq{})
	require.Len(t, p.Units, 8*DealAhead*2, "dealt %d, want %d", len(p.Units), 8*DealAhead*2)
	for m, n := range perMember(w.s) {
		require.Equal(t, DealAhead*2, n, "%s holds %d, want DealAhead times 2", m, n)
	}
}

// T3, the present tick's deal, fills eight members of width 64 to DealAhead
// times their width in the one plan, past TickMaxMoves's 200-unit bound of the
// other parts only by TickMaxDeal's.
func TestTickDealFillsTheFleetToDealAheadTimesWidth(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	for _, m := range widthMembers {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: 64}))
	}
	for _, st := range []string{"s1", "s2", "s3"} {
		w.must(Add(w.s, AddReq{Stream: st, Count: 400}))
	}
	p, due := TickDeal(w.s, TickReq{})
	require.Len(t, p.Units, 8*DealAhead*64, "dealt %d (due %d), want the room of %d", len(p.Units), due, 8*DealAhead*64)
	require.Zero(t, due, "dealt %d (due %d), want the room of %d", len(p.Units), due, 8*DealAhead*64)
	w.must(p)
	for m, n := range perMember(w.s) {
		require.Equal(t, DealAhead*64, n, "%s holds %d, want DealAhead times its width 64", m, n)
	}
	p, _ = TickDeal(w.s, TickReq{})
	require.Empty(t, p.Units, "a fleet at DealAhead times its width is dealt %d more", len(p.Units))
}

// The width is the control card's: set by up, kept by an up that names none,
// changed by one that names another, refused past MaxWidth; the default is 64.
func TestTheWidthIsTheControlCards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 8}))
	a, b := w.s.Width("m1"), w.s.Width("m2")
	require.Equal(t, DefaultWidth, a, "widths %d and %d", a, b)
	require.Equal(t, 8, b, "widths %d and %d", a, b)
	w.must(FleetStep(w.s, FleetReq{Op: "release", Member: "m2"}))
	n := w.s.Width("m2")
	require.Equal(t, 8, n, "an up naming no width changed it to %d", n)
	w.must(FleetStep(w.s, FleetReq{Op: "release", Member: "m2", Width: 16}))
	n = w.s.Width("m2")
	require.Equal(t, 16, n, "width %d, want 16", n)
	p := FleetStep(w.s, FleetReq{Op: "up", Member: "m3", Width: MaxWidth + 1})
	require.Len(t, p.Refused, 1, "a width past MaxWidth: %+v", p)
	require.Empty(t, p.Units, "a width past MaxWidth: %+v", p)
	specs, err := ParseMembers("m1:64,m2,m3:2")
	require.NoError(t, err, "ParseMembers: %+v %v", specs, err)
	require.Len(t, specs, 3, "ParseMembers: %+v %v", specs, err)
	require.Equal(t, MemberSpec{"m1", 64}, specs[0], "ParseMembers: %+v %v", specs, err)
	require.Equal(t, MemberSpec{"m2", 0}, specs[1], "ParseMembers: %+v %v", specs, err)
	require.Equal(t, MemberSpec{"m3", 2}, specs[2], "ParseMembers: %+v %v", specs, err)
	for _, bad := range []string{"m1:0", "m1:x", "m1:1025", "m.1:4"} {
		_, err = ParseMembers(bad)
		assert.Error(t, err, "ParseMembers(%q) was taken", bad)
	}
	// a clear keeps the width with the member
	sh := ShapeOf(w.s)
	require.Equal(t, "16", sh.Width["m2"], "the shape's widths: %v", sh.Width)
	require.Empty(t, sh.Width["m1"], "the shape's widths: %v", sh.Width)
}
