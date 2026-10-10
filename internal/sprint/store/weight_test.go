package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A card's weight is the cards transitively waiting on it (weight.go): on a four-card chain
// the root's judgment is listed first and where names it; a leaf with nothing behind it is
// unchanged; add and drop write each card's weight on it as `behind`. The deal, the ask and
// the batch keep the modelled order (stream turns, work order) until the reference model
// orders by weight.
func TestACriticalRootIsDealtReadAndJudgedFirst(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"leaf"}, Brief: briefOf("flash", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"root"}, Brief: briefOf("flash", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"mid"}, Needs: []string{"root"}, Brief: briefOf("flash", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"tip"}, Needs: []string{"mid"}, Brief: briefOf("flash", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"end"}, Needs: []string{"tip"}, Brief: briefOf("flash", "")}))
	w := sprint.Weights(h.snap())
	assert.Equal(t, map[string]int{"leaf": 0, "root": 3, "mid": 2, "tip": 1, "end": 0}, w)
	h.startMachine()
	h.machine()
	h.machine()
	require.Equal(t, sprint.Working, h.state("root"))
	require.Equal(t, sprint.Working, h.state("leaf"))
	assert.Equal(t, "3", h.snap().Work.Card("root").F(sprint.FieldBehind), "the add writes the weight on the primaries it changes")
	assert.Equal(t, "", h.snap().Work.Card("leaf").F(sprint.FieldBehind), "a leaf with nothing behind it is unchanged")
	assert.Equal(t, "flash", h.workCards()["root.w1"].F(sprint.FieldTier), "three behind is not critical: the brief's tier")
	// both in review: the ask reads the root first, and its judgment lists first though newer
	h.finishAttempt("root", false, "h1")
	h.finishAttempt("leaf", true, "")
	h.tick(1)
	h.machine()
	rc := h.snap().Readers.Of("root")
	require.NotEmpty(t, rc)
	h.must(ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc[0].Row, Verdict: "broken", Finding: "f:1", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	v, err := h.st.Inbox(h.ctx, sprint.DeadlineJudgment, 0, 1000)
	require.NoError(t, err)
	var types []string
	for _, g := range v.Groups {
		// The backup edge is its own judgment (sprint.TickBackup). This test is
		// the root's read judgment listing first among the card judgments.
		if g.Kind == sprint.Judgment && g.Type != sprint.NReadsBackedUp && g.Type != sprint.NReadsClear && g.Type != sprint.NMergesBackedUp && g.Type != sprint.NMergesClear {
			types = append(types, g.Type)
		}
	}
	require.Len(t, types, 2)
	assert.Equal(t, sprint.NReadBroken, types[0], "the root's judgment first, though the leaf's is older: %v", types)
	behind := 0
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment && g.Type == sprint.NReadBroken {
			behind = g.Behind
			break
		}
	}
	assert.Equal(t, 3, behind)
	assert.Equal(t, []sprint.CriticalCard{{ID: "root", Behind: 3, State: sprint.Review}, {ID: "mid", Behind: 2, State: sprint.Waiting}, {ID: "tip", Behind: 1, State: sprint.Waiting}}, sprint.Critical(h.snap(), 5))
	assert.Equal(t, "critical: root 3 behind, review; mid 2 behind, waiting; tip 1 behind, waiting", sprint.CriticalLine(sprint.Critical(h.snap(), 5)))
	// a drop writes the weights it changes
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"end"}}, Reason: "gone", Who: "tester"}))
	assert.Equal(t, "2", h.snap().Work.Card("root").F(sprint.FieldBehind))
	assert.Equal(t, "", h.snap().Work.Card("tip").F(sprint.FieldBehind), "nothing behind it now: the field is unset")
	h.clean("the chain weighed")
}

// A card with CriticalBehind or more behind it starts on a pro route whatever its brief
// says, with pro's deadline and budget, and is never dealt below it.
func TestACriticalCardStartsOnPro(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldBehind: "10"})
	h.must(DealStep(sprint.DealReq{}))
	wc := h.workCards()["s1-1.w1"]
	require.NotNil(t, wc)
	assert.Equal(t, "pro-a", wc.F(sprint.FieldRoute), "a critical card draws a pro route")
	assert.Equal(t, "pro", wc.F(sprint.FieldTier))
	now, ceiling := sprint.CardTiers(h.snap().Work.Card("s1-1"))
	assert.Equal(t, []string{"pro", "pro"}, []string{now, ceiling}, "never dealt below pro")
	line := sprint.CriticalLine([]sprint.CriticalCard{{ID: "ftsync-t1-merge", Behind: 140, State: sprint.Working}})
	assert.Equal(t, "critical: ftsync-t1-merge 140 behind, working", line)
}

// A need the card waived does not hold it (unmet skips it, so the card can land while the
// need is open), so it adds nothing to that need's weight: a need dropped, waived and then
// added again weighs 0 behind the card that no longer waits on it.
func TestAWaivedNeedWeighsNothingBehindItsWaiter(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"need"}, Brief: briefOf("flash", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"need"}, Brief: briefOf("flash", "")}))
	require.Equal(t, map[string]int{"need": 1, "waiter": 0}, sprint.Weights(h.snap()))
	h.setPrimary("waiter", map[string]string{"waived": "need"})
	assert.Equal(t, map[string]int{"need": 0, "waiter": 0}, sprint.Weights(h.snap()), "the waived need weighs nothing")
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"other"}, Brief: briefOf("flash", "")}))
	assert.Equal(t, "", h.snap().Work.Card("need").F(sprint.FieldBehind), "the next add rewrites the weight it changed: the field is unset")
	h.clean("a waived need weighed")
}
