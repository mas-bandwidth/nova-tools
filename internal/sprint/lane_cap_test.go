package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's lane ended at its tier's cap (lane_cap.go, a-lane-is-capped-by-its-tier.w1):
// the first capped finish re-deals the card once at the next tier up, its failed count
// untouched, and the next deal cuts its attempt on that tier; a second cap is failed work.
// Each capped take's cap and overrun are on its cost record and its work card.
func TestACappedLaneIsReDealtOnceAtTheNextTierUp(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash,pro,heavy"}}
	amy := FriendRow("amy")
	dealWith(w, seats...)
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	capped := "friend amy HOLD: nova-friend lane 1 of amy finished card s1-1.w1: capped at 15m0s (tier flash, overrun 3s): the card's wall reached its tier's cap and the daemon ended the lane, exit -1, wall 15m3s; no pushed head found."

	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: amy, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: capped}))
	pr := w.s.Primary("s1-1")
	assert.Equal(t, Ready, w.s.StateOf("s1-1"), "re-dealt: back to ready for the next deal")
	assert.Equal(t, "pro", pr.F(FieldTierNow), "one tier up")
	assert.Equal(t, "flash -> pro", pr.F(FieldCapRedealt))
	assert.Empty(t, pr.F("failed"), "the cap is no failure the first time")
	assert.Contains(t, pr.F("why"), "capped at 15m0s on flash (overrun 3s): re-dealt once at the next tier up, pro")
	assert.Contains(t, pr.F(FieldCostRecord+"s1-1.w1#g1"), "end=capped at=")
	assert.Contains(t, pr.F(FieldCostRecord+"s1-1.w1#g1"), "lane_cap=15m0s lane_overrun=3s")
	wc := w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, DoneFailed, wc.Col)
	assert.Equal(t, "15m0s", wc.F(FieldLaneCap))
	assert.Equal(t, "3s", wc.F(FieldLaneOverrun))
	cons := CardCostOf(pr)
	require.NotEmpty(t, cons.Consumers)
	assert.Equal(t, "15m0s", cons.Consumers[0].Cap, "the cost record reads back with its cap")
	assert.Empty(t, Check(w.s, nil))

	dealWith(w, seats...)
	w2 := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, w2, "the next deal cuts the next attempt")
	assert.Equal(t, amy, w2.Row)
	assert.Equal(t, "pro", DealtTier(w2, w.s.Primary("s1-1")), "on the tier the cap raised it to")

	again := "friend amy HOLD: nova-friend lane 1 of amy finished card s1-1.w2: capped at 45m0s (tier pro, overrun 1s): the card's wall reached its tier's cap and the daemon ended the lane, exit -1, wall 45m1s; no pushed head found."
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w2"}}, As: amy, Gens: gensOf(w.s, "s1-1.w2"), Failed: true, Report: again}))
	pr = w.s.Primary("s1-1")
	assert.Equal(t, Review, w.s.StateOf("s1-1"), "the second cap is failed work")
	assert.Equal(t, "1", pr.F("failed"))
	assert.Equal(t, "pro", pr.F(FieldTierNow), "no second re-deal")
	assert.Contains(t, pr.F(FieldCostRecord+"s1-1.w2#g1"), "lane_cap=45m0s lane_overrun=1s")
	assert.Equal(t, "45m0s", w.s.Fleet.Card("s1-1.w2").F(FieldLaneCap))
	assert.Empty(t, Check(w.s, nil))
}

// A report that names no cap is no capped finish, and one whose words are broken is none.
func TestParseLaneCapReadsOnlyTheLanesWords(t *testing.T) {
	t.Parallel()
	for _, r := range []string{"", "friend amy FAIL: the run exited 3", "capped at soon (tier flash, overrun 0s)", "capped at 0s (tier flash, overrun 0s)", "we capped at 15m0s the budget"} {
		_, ok := ParseLaneCap(r)
		assert.False(t, ok, r)
	}
	lc, ok := ParseLaneCap("capped at 2h30m0s (tier -, overrun 0s)")
	require.True(t, ok)
	assert.Equal(t, LaneCap{Cap: 150 * 60 * 1e9}, lc)
}
