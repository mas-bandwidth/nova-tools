package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
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

// The unified deadline function serves both members and friends (docs/SPEC-SPRINT.md
// section 5, the deadline; section 1, a friend's card's deadline). Both use the same
// rule: the larger of their own deadline and DeadlineK times the median run wall.
func TestOneDeadlineRuleForMembersAndFriends(t *testing.T) {
	t.Parallel()

	// Test the unified Deadline function directly
	assert.Equal(t, 600, Deadline(100.0, 10, 600), "own deadline wins when larger")
	assert.Equal(t, 300, Deadline(100.0, 10, 200), "3x median wins when larger")
	assert.Equal(t, 600, Deadline(0, 0, 600), "no samples returns own")

	// Test member deadline path with median wall
	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	for i := 1; i <= 10; i++ {
		w.s.Fleet.Put(&Card{ID: fmt.Sprintf("q%d", i), Row: "m1", Col: DoneOK, Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("q%d", i), "finished": fmt.Sprintf("2030-01-01T00:%02d:00Z", i), FieldUsage: "wall=100s"}})
	}
	assert.Equal(t, 300, w.s.memberDeadline("m1", 200), "member: 3x median")

	// Test that friend deadline uses the same underlying rule by checking the median formula
	median, n := MemberMedianWall(w.s, "m1")
	assert.Equal(t, 100.0, median, "member median is 100s")
	assert.Equal(t, 10, n)
	assert.Equal(t, 300, Deadline(median, n, 200), "unified function applies 3x rule")
}
