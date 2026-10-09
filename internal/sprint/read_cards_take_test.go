package sprint_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/require"
)

// A manual take reads the stored read-cards setting before applying the same
// half-slot accounting as the tick (docs/SPEC-SPRINT.md section 6,
// "A read is a consumer card"). At width four, two reads in working leave
// three work slots; loading Fleet alone would incorrectly leave only two.
func TestManualTakeUsesStoredReadCardsSettingForHalfSlots(t *testing.T) {
	t.Parallel()
	r := newReadCardsRig(t)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		r.toReview(id, proBrief)
	}

	var readIDs []string
	readGens := map[string]int{}
	for _, c := range r.snap().Fleet.Cell("m1", sprint.Ready) {
		if c.F("kind") == "read" {
			readIDs = append(readIDs, c.ID)
			readGens[c.ID] = c.Int("gen")
		}
	}
	require.Len(t, readIDs, 2, "two reviews worked by other members give m1 two read cards")
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: readIDs}, Gens: readGens, Who: "m1"}))
	require.Equal(t, 2, r.snap().Fleet.Count("m1", sprint.Working), "both reads hold half slots while working")

	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 12, Brief: proBrief}))
	r.tick()
	s := r.snap()
	var readyWork []*sprint.Card
	for _, c := range s.Fleet.Cell("m1", sprint.Ready) {
		if c.F("kind") != "read" {
			readyWork = append(readyWork, c)
		}
	}
	require.GreaterOrEqual(t, len(readyWork), 3, "m1 has three ready work cards to fit in its remaining half-slots")

	res := r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 3}, Who: "m1"}))
	require.Len(t, res.Moved, 3, "the stored read-cards setting allows three work cards beside two active reads")
	s = r.snap()
	require.Equal(t, 3, s.Fleet.Count("m1", sprint.Working)-2, "three work cards join the two reads without exceeding width four")
}
