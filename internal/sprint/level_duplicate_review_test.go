package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two loaded readers hold the same newest primary, and the third is idle.
// Planned target placements must exclude later moves in that same plan:
// retiring both siblings and creating one identical target cannot be applied.
func TestLevelReviewSiblingMovesNeverCreateTheSameTargetTwice(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	toReview(w, "s1-1", "s1-2", "s1-3")
	for _, rd := range []string{"reader-a", "reader-b"} {
		for _, primary := range []string{"s1-1", "s1-2"} {
			rc := putRead(w, primary, 1, rd, Reading)
			rc.Fields["asked"], rc.Fields["begun"] = stamp(t0), stamp(t0)
		}
		rc := putRead(w, "s1-3", 1, rd, Asked)
		rc.Fields["asked"] = stamp(t0)
	}
	p, _ := TickLevelReads(w.s, TickReq{})
	require.NotEmpty(t, p.Units, "the idle reader receives a read")
	created := map[string]bool{}
	for _, u := range p.Units {
		for _, change := range u.Changes {
			if change.Table != Readers || change.Entry.Create == nil {
				continue
			}
			assert.False(t, created[change.Entry.ID], "one rebalance creates %s twice, so its two sibling reads cannot remain distinct", change.Entry.ID)
			created[change.Entry.ID] = true
		}
	}
	w.must(p)
	assert.Len(t, liveReadsAt(w.s, w.s.Work.Card("s1-3"), 1), 2, "the primary retains two distinct outstanding reads")
}
