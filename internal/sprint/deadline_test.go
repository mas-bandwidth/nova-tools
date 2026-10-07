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

// One deadline rule serves a member's deal and a friend's take (docs/SPEC-SPRINT.md
// section 5, the deadline; deadline.go): a card placed on either row gets the larger of
// its own and DeadlineK times the row's median run wall over its last DeadlineSamples ok
// attempts, measured by RowMedianWall; a member's pin stands over it and a friend's own
// is DeadlineUnfinished. Both tables' rows are served by the one rule.
func TestOneDeadlineRuleForMembersAndFriends(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	m, amy := "m1", FriendRow("amy")
	put := func(row, id, finished, wall string) {
		w.s.Fleet.Put(&Card{ID: id, Row: row, Col: DoneOK, Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": id, "attempt": "1", "ok": "yes",
				"finished": finished, FieldUsage: "wall=" + wall}})
	}
	// the same three ok attempts on the member's row and her row: one median, 4000 s
	for i, wall := range []string{"3000s", "4000s", "5000s"} {
		f := fmt.Sprintf("2030-01-01T00:0%d:00Z", i)
		put(m, fmt.Sprintf("m%d.w1", i), f, wall)
		put(amy, fmt.Sprintf("a%d.w1", i), f, wall)
	}
	mm, mn := RowMedianWall(w.s, m)
	fm, fn := RowMedianWall(w.s, amy)
	assert.Equal(t, 4000.0, mm)
	assert.Equal(t, mm, fm, "the one rule measures a member's row and a friend's the same")
	assert.Equal(t, mn, fn)

	// the one rule: three times the median on either row, the card's own when it is larger
	assert.Equal(t, 12000, w.s.rowDeadline(m, 600, 0), "a member's deal")
	assert.Equal(t, 12000, w.s.rowDeadline(amy, 600, 0), "her take is the same rule")
	assert.Equal(t, 20000, w.s.rowDeadline(m, 20000, 0), "the card's own when it is larger")
	assert.Equal(t, 20000, w.s.rowDeadline(amy, 20000, 0))

	// both tables: the member's deal carries it on the work card, her take on her deadline
	work := map[string]string{"kind": "work", FieldDeadline: "600"}
	w.s.dealDeadline(m, work)
	assert.Equal(t, "12000", work[FieldDeadline])
	assert.Equal(t, "600", work[FieldOwnDeadline], "the card's own kept for a later move")
	set, unset := friendDeadline(w.s, "amy")
	assert.Empty(t, unset)
	assert.Equal(t, "12000", set[FieldFriendDeadline], "the friend's card carries the one rule")

	// a member's row may pin it, and the pin is the deadline whatever the card's
	pinned := 45 * 60 // seconds, as fleet up --deadline 45m writes it
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Deadline: pinned}))
	assert.Equal(t, 2700, w.s.memberDeadline(m, 600), "the pin, whatever the card's")

	// a friend has no pin: her own, DeadlineUnfinished, holds under three times her median,
	// and she carries none until her first ok attempt
	put(FriendRow("fast"), "fast.w1", "2030-01-01T00:10:00Z", "100s")
	set, _ = friendDeadline(w.s, "fast")
	assert.Equal(t, "7200", set[FieldFriendDeadline], "three times 100 is under her own")
	w2 := newWorld(t, "reader-a")
	assert.Equal(t, 600, w2.s.rowDeadline("m9", 600, 0), "no ok attempt: the card's own")
	_, unset = friendDeadline(w2.s, "nobody")
	assert.Equal(t, []string{FieldFriendDeadline}, unset, "no ok attempt: she carries none")
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
