package sprint

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The member's median run wall is over its last DeadlineSamples ok attempts, newest
// finished first: an older attempt is not counted (deadline.go).
func TestTheMedianWallIsOverTheLastFiftyOkAttempts(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	put := func(i int, finished, wall string) {
		w.s.Fleet.Put(&Card{ID: fmt.Sprintf("p%d.w1", i), Row: "m1", Col: DoneOK, Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("p%d", i), "attempt": "1", "ok": "yes", "finished": finished, FieldUsage: "wall=" + wall}})
	}
	// the oldest ran an hour; the fifty after it 100 s each
	put(0, "2030-01-01T00:00:00Z", "3600s")
	for i := 1; i <= DeadlineSamples; i++ {
		put(i, fmt.Sprintf("2030-01-01T01:%02d:00Z", i%60), "100s")
	}
	w.s.Fleet.cells = nil
	median, n := MemberMedianWall(w.s, "m1")
	assert.Equal(t, DeadlineSamples, n, "the last fifty")
	assert.Equal(t, 100.0, median, "the hour-long attempt is older than the window")
	assert.Equal(t, 600, w.s.memberDeadline("m1", 600), "the card's own deadline when it is the larger")
	assert.Equal(t, 300, w.s.memberDeadline("m1", 200), "three times the median when that is")
	for _, text := range []string{"0", "-1m", "25h", "soon"} {
		_, _, err := ParseDeadline(text)
		assert.Error(t, err, text)
	}
	secs, off, err := ParseDeadline("45m")
	assert.NoError(t, err)
	assert.Equal(t, 2700, secs)
	assert.False(t, off)
	_, off, err = ParseDeadline("default")
	assert.NoError(t, err)
	assert.True(t, off)
}

// The median is measured once a member for the done-ok cell the fleet table holds, not
// once a card dealt, and a card put on the table is a new cell, measured again
// (deadline.go, medianWalls).
func TestTheMedianWallIsMeasuredOnceACellAndAgainAfterAPut(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	member := "median-memo-m1"
	put := func(i int, wall string) {
		w.s.Fleet.Put(&Card{ID: fmt.Sprintf("q%d.w1", i), Row: member, Col: DoneOK, Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("q%d", i), "attempt": "1", "ok": "yes", "finished": fmt.Sprintf("2030-01-01T00:%02d:00Z", i), FieldUsage: "wall=" + wall}})
	}
	put(1, "100s")
	put(2, "100s")
	median, n := MemberMedianWall(w.s, member)
	assert.Equal(t, 100.0, median)
	assert.Equal(t, 2, n)
	first := &w.s.Fleet.Cell(member, DoneOK)[0]
	again, _ := MemberMedianWall(w.s, member)
	assert.Equal(t, 100.0, again, "the same cell, the same median")
	assert.Same(t, first, &w.s.Fleet.Cell(member, DoneOK)[0], "asking again does not rebuild the cell")
	put(3, "400s")
	put(4, "400s")
	put(5, "400s")
	median, n = MemberMedianWall(w.s, member)
	assert.Equal(t, 400.0, median, "a card put is a new cell, measured again")
	assert.Equal(t, 5, n)
	empty := NewTable(Fleet)
	w.s.Fleet = empty
	median, n = MemberMedianWall(w.s, member)
	assert.Zero(t, median, "a member with no ok attempts has no median")
	assert.Zero(t, n)
}

// The unified Deadline function serves both members and friends with the same rule.
func TestOneDeadlineRuleForMembersAndFriends(t *testing.T) {
	t.Parallel()
	// Test the unified Deadline function directly
	median, n := 100.0, 50
	assert.Equal(t, 300, Deadline(median, n, 200), "three times the median when that is larger")
	assert.Equal(t, 600, Deadline(median, n, 600), "the own deadline when it is larger")
	assert.Equal(t, 200, Deadline(0, 0, 200), "the own deadline when n is zero")
	// Test member deadline path
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	for i := 1; i <= DeadlineSamples; i++ {
		w.s.Fleet.Put(&Card{ID: fmt.Sprintf("p%d.w1", i), Row: "m1", Col: DoneOK, Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("p%d", i), "attempt": "1", "ok": "yes", "finished": fmt.Sprintf("2030-01-01T01:%02d:00Z", i%60), FieldUsage: "wall=100s"}})
	}
	assert.Equal(t, 300, w.s.memberDeadline("m1", 200), "member deadline uses unified function")
	assert.Equal(t, 600, w.s.memberDeadline("m1", 600), "member deadline respects own when larger")
}

// cellsMeasured is how many times member's done-ok cell has been measured.
// The counter is process-global (medianWalls), so the caller holds its lock.
func cellsMeasured(member string) int {
	medianWalls.mu.Lock()
	defer medianWalls.mu.Unlock()
	return medianWalls.measured[member]
}

// TickDeal asks the member's median for every card it deals, and measures the
// done-ok cell once across those asks and across plans of the same snapshot
// (deadline.go). The plan is not applied: applying it can put cards and
// legitimately change the cell. The member names are this test's alone.
func TestTickDealMeasuresEachDoneOKCellOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	members := []string{"deal-once-a", "deal-once-b"}
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	w.s.Routes = []Route{{Name: "flash-deal-once", Tier: "flash", Provider: "prov-deal-once", Model: "model-deal-once", Tokens: 1000, Deadline: int(10 * time.Minute / time.Second), Enabled: true}}
	for _, m := range members {
		for i := 1; i <= 3; i++ {
			w.s.Fleet.Put(&Card{ID: fmt.Sprintf("%s-h%d", m, i), Row: m, Col: DoneOK, Rev: 1,
				Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("%s-p%d", m, i), "attempt": "1", "ok": "yes", "finished": fmt.Sprintf("2030-01-01T00:%02d:00Z", i), FieldUsage: "wall=100s"}})
		}
	}
	w.must(Add(w.s, AddReq{Stream: "s-deal-once", Count: 4}))

	before := map[string]int{}
	cell0 := map[string]**Card{}
	for _, m := range members {
		cell := w.s.Fleet.Cell(m, DoneOK)
		require.Greater(t, len(cell), 1, "%s's done-ok cell", m)
		cell0[m] = &cell[0]
		before[m] = cellsMeasured(m)
	}
	var units int
	for range 3 {
		p, _ := TickDeal(w.s, TickReq{})
		require.Greater(t, len(p.Units), 1, "one deal planned %d units", len(p.Units))
		units = len(p.Units)
	}
	for _, m := range members {
		cell := w.s.Fleet.Cell(m, DoneOK)
		require.Same(t, cell0[m], &cell[0], "TickDeal changed %s's done-ok cell", m)
		got := cellsMeasured(m) - before[m]
		require.Equal(t, 1, got, "%s's done-ok cell was measured %d times across three deals of %d units", m, got, units)
	}
}
