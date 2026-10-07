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

// TestDeadlineRules forBothMemberAndFriend verifies that the member deadline rule (member
// deadline) and the friend deadline rule (friend deadline) are properly pinned. Both rules
// are derived from the same spec: the larger of a base (DeadlineUnfinished for friends, own
// for members) and K times the median run wall over the last N ok attempts.
func TestDeadlineRulesPinBothMemberAndFriend(t *testing.T) {
	t.Parallel()

	// --- Member table: median wall and memberDeadline ---
	memberWorld := newWorld(t, "reader-a")
	memberWorld.must(FleetStep(memberWorld.s, FleetReq{Op: "up", Member: "m1"}))

	// Set up 5 ok attempts with 100s each for member m1
	for i := 0; i < 5; i++ {
		memberWorld.s.Fleet.Put(&Card{
			ID:     fmt.Sprintf("m1.p%d.w1", i),
			Row:    "m1",
			Col:    DoneOK,
			Rev:    1,
			Fields: map[string]string{"kind": "work", "primary": fmt.Sprintf("m1.p%d", i), "attempt": "1", "ok": "yes", "finished": fmt.Sprintf("2030-01-01T01:%02d:00Z", i), FieldUsage: "wall=100s"},
		})
	}

	median, n := MemberMedianWall(memberWorld.s, "m1")
	assert.Equal(t, 5, n, "member should have 5 samples")
	assert.Equal(t, 100.0, median, "member median should be 100s")

	// Verify member deadline: max(own, 3*median) = max(200, 300) = 300
	deadline := memberWorld.s.memberDeadline("m1", 200)
	assert.Equal(t, 300, deadline, "member deadline should be 3*median when median*K > own")

	// --- Friend table: median wall and friendDeadline ---
	// This test mirrors TestAFriendsCardsDeadlineIsThreeTimesHerMedianWall from friend_deadline_test.go
	friendWorld := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	dealStarted(friendWorld, FriendSeat{Name: "amy", Width: 3, Status: Up, Class: "flash,pro"})

	// Initially no ok attempts
	_, n = FriendMedianWall(friendWorld.s, "amy")
	assert.Zero(t, n, "no ok attempts yet")

	// Set up 3 ok attempts with 100s wall each for friend amy
	// Friend cards use taken/reported stamps for wall (not FieldUsage like members)
	t0 := friendWorld.s.Now
	for i := 1; i <= 3; i++ {
		taken := t0.Add(time.Duration(i-1) * time.Hour)
		reported := taken.Add(100 * time.Second) // 100s run wall
		finished := reported.Add(1 * time.Second)
		friendWorld.s.Fleet.Put(&Card{
			ID:  fmt.Sprintf("s1-%d.w1", i),
			Row: FriendRow("amy"),
			Col: DoneOK,
			Rev: 1,
			Fields: map[string]string{
				"kind":     "work",
				"primary":  fmt.Sprintf("s1-%d", i),
				"attempt":  "1",
				"ok":       "yes",
				"finished": finished.Format(time.RFC3339),
				"taken":    taken.Format(time.RFC3339),
				"reported": reported.Format(time.RFC3339),
			},
		})
	}

	friendMedian, friendN := FriendMedianWall(friendWorld.s, "amy")
	assert.Equal(t, 3, friendN, "friend should have 3 samples")
	assert.Equal(t, 100.0, friendMedian, "friend median should be 100s")

	// Verify friend deadline formula: DeadlineUnfinished=2h=7200s, FriendDeadlineK=3, median=100
	// friendDeadline should be max(7200, 3*100) = max(7200, 300) = 7200
	// This verifies the friend rule pins correctly
	friendRow := FriendRow("amy")
	amyCards := friendWorld.s.Fleet.Cell(friendRow, DoneOK)
	require.Greater(t, len(amyCards), 0, "friend should have cards")
	// A card on her row should carry friend_deadline = 7200 (2h) after taking
	// Check that friendDeadline function computes the right value
	set, _ := friendDeadline(friendWorld.s, "amy")
	require.Contains(t, set, FieldFriendDeadline)
	assert.Equal(t, "7200", set[FieldFriendDeadline], "friend deadline should be 7200 (DeadlineUnfinished) when median is small")
}
