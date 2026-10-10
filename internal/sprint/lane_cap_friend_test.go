package sprint_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// cappedReport is the report a friend's capped lane finishes with, in the lane's own words
// (pkg/friend, TestALaneIsCappedByItsTier holds that the lane writes them).
func cappedReport() string {
	return "friend bob HOLD: nova-friend lane 1 of bob finished card c1: " + friend.CappedWords(2*time.Minute, "flash", 3*time.Second) +
		": the card's wall reached its tier's cap and the daemon ended the lane"
}

// A friend's lane capped by its card's tier (a-lane-is-capped-by-its-tier.w1), as the sprint
// reads the finish (it was the second half of pkg/friend's TestALaneIsCappedByItsTier):
// the first cap re-deals the card once more at the next tier up, its failed count
// untouched, and the cap and the overrun are on the take's cost record; a second cap counts
// as a failure.
func TestACappedFriendLaneIsReDealtOnceAtTheNextTierUp(t *testing.T) {
	t.Parallel()
	s, finish := cappedSprint(t, "", cappedReport())
	p := sprint.Finish(s, finish)
	require.Empty(t, p.Refused, "%+v", p.Refused)
	require.Len(t, p.Units, 1)
	wc, pr := capEntryOf(p.Units[0], "c1"), capEntryOf(p.Units[0], "p1")
	require.NotNil(t, wc.Move)
	assert.Equal(t, sprint.DoneFailed, wc.Move.Col)
	assert.Equal(t, "2m0s", wc.Set[sprint.FieldLaneCap])
	require.NotNil(t, pr.Move)
	assert.Equal(t, sprint.Ready, pr.Move.Col, "re-dealt: the primary goes back to ready for the next deal")
	assert.Equal(t, "pro", pr.Set[sprint.FieldTierNow], "at the next tier up")
	assert.Equal(t, "flash -> pro", pr.Set[sprint.FieldCapRedealt])
	assert.Empty(t, pr.Set["failed"], "a cap re-dealt is no failure")
	rec := pr.Set[sprint.FieldCostRecord+"c1#g1"]
	assert.Contains(t, rec, "end=capped")
	assert.Contains(t, rec, "lane_cap=2m0s lane_overrun=")
	assert.Contains(t, p.Units[0].Moved, "re-dealt once at the next tier up, pro")

	// a card the cap re-dealt once already: the second cap is a failure
	s, finish = cappedSprint(t, "flash -> pro", cappedReport())
	p = sprint.Finish(s, finish)
	require.Empty(t, p.Refused, "%+v", p.Refused)
	require.Len(t, p.Units, 1)
	pr = capEntryOf(p.Units[0], "p1")
	require.NotNil(t, pr.Move)
	assert.Equal(t, sprint.Review, pr.Move.Col)
	assert.Equal(t, "1", pr.Set["failed"], "the second cap counts as a failure")
	assert.Contains(t, pr.Set[sprint.FieldCostRecord+"c1#g1"], "lane_cap=2m0s")
	assert.Equal(t, "2m0s", capEntryOf(p.Units[0], "c1").Set[sprint.FieldLaneCap])
}

// The sprint reads the words a capped lane writes (friend.CappedWords): its cap, its
// overrun and its tier.
func TestTheSprintReadsTheWordsACappedLaneWrites(t *testing.T) {
	t.Parallel()
	words := friend.CappedWords(15*time.Minute, "flash", 2400*time.Millisecond)
	lc, ok := sprint.ParseLaneCap("friend bob HOLD: nova-friend lane 1 of bob finished card c1: " + words + ": the card's wall ...")
	require.True(t, ok, "the sprint reads the words the lane writes")
	assert.Equal(t, sprint.LaneCap{Cap: 15 * time.Minute, Overrun: 2 * time.Second, Tier: "flash"}, lc)
}

// cappedSprint is a sprint with the friend bob's work card c1 working for the primary p1,
// on flash, its cap re-deal spent when redealt says so, and bob's finish of it with report.
func cappedSprint(t *testing.T, redealt, report string) (*sprint.Snapshot, sprint.FinishReq) {
	t.Helper()
	s := &sprint.Snapshot{Now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), Work: sprint.NewTable(sprint.Work), Readers: sprint.NewTable(sprint.Readers),
		Merge: sprint.NewTable(sprint.Merge), Fleet: sprint.NewTable(sprint.Fleet), Coordinator: "coordinator", Actor: "coordinator"}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{sprint.FriendRow("bob")})
	pr := map[string]string{"kind": "primary", "stream": "s1", "work": "c1", "attempt": "1", "brief": "p1: a card\n\nThe task."}
	if redealt != "" {
		pr[sprint.FieldCapRedealt], pr[sprint.FieldTierNow] = redealt, "pro"
	}
	s.Work.Put(&sprint.Card{ID: "p1", Row: "s1", Col: sprint.Working, Fields: pr})
	s.Fleet.Put(&sprint.Card{ID: "c1", Row: sprint.FriendRow("bob"), Col: sprint.Working, Fields: map[string]string{
		"kind": "work", "primary": "p1", "stream": "s1", "attempt": "1", "gen": "1", "member": sprint.FriendRow("bob")}})
	return s, sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"c1"}}, As: sprint.FriendRow("bob"), Gens: map[string]int{"c1": 1},
		Failed: true, Branch: "sprint/c1.g1.e15", Report: report}
}

// capEntryOf is the change a unit makes to the card id.
func capEntryOf(u sprint.Unit, id string) ntable.BatchMemberEntry {
	i := slices.IndexFunc(u.Changes, func(c sprint.Change) bool { return c.Entry.ID == id })
	if i < 0 {
		return ntable.BatchMemberEntry{}
	}
	return u.Changes[i].Entry
}
