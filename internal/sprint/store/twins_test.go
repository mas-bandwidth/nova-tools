package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Twins inherit (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin"; the
// coordinator, 2026-10-04: 311 cards sat behind 21 judgments "a primary is blocked on something
// dropped" after cards were re-cut as twins, each a drop of the old id and an add of a new
// one). add --replaces <old> is that pair as one step: the new card takes over every edge
// where a waiting card needs the old id, the old id is dropped "replaced by <new>", and no
// blocked judgment is raised for it. relink <old> <new> repairs the edges a drop and an add
// left, and closes the blocked judgments of the pair. On the mem twin, injected clock, no
// socket; the model is tla/SprintRules.tla (TwinsInherit, NoDanglingNeed).

// twins is a harness with the old card in s1 and three waiting cards in s2 that need it:
// dep1 and dep2 need old alone, dep3 needs old and other.
func twins(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.setup(0)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"old", "other"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"dep1", "dep2"}, Needs: []string{"old"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"dep3"}, Needs: []string{"old", "other"}}))
	h.clean("twins")
	return h
}

func TestAddReplacesTakesOverEveryEdgeOfTheOldCard(t *testing.T) {
	t.Parallel()
	h := twins(t)
	res := h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"old-tb"}, Replaces: []string{"old"}, Who: "tester"}))
	s := h.snap()
	old := h.record("old")
	require.NotNil(t, old)
	assert.False(t, old.Placed(), "the old card is off the table")
	assert.Equal(t, "dropped", old.F("outcome"))
	assert.Equal(t, "replaced by old-tb", old.F("reason"))
	assert.Equal(t, "old-tb", s.Work.Card("dep1").F("needs"))
	assert.Equal(t, "old-tb", s.Work.Card("dep2").F("needs"))
	assert.Equal(t, "old-tb,other", s.Work.Card("dep3").F("needs"), "the other need is kept, in its place")
	for _, id := range []string{"dep1", "dep2", "dep3"} {
		assert.Equal(t, sprint.Waiting, s.Work.Card(id).Col, "%s still waits, now for its twin", id)
	}
	assert.Empty(t, h.nAllNotes(sprint.NBlocked), "no blocked judgment is raised for a replaced card")
	assert.Equal(t, "3", s.Work.Card("old-tb").F(sprint.FieldBehind), "the twin carries the weight of what waits on it")
	assert.Equal(t, "1", s.Work.Card("other").F(sprint.FieldBehind))
	assert.True(t, strings.Contains(strings.Join(res.Moved, "\n"), "dep1 needs old -> old-tb"), "the move lines say the relink: %v", res.Moved)
	needs, _ := sprint.NeedsOf(s, "dep3")
	require.Len(t, needs, 2)
	assert.Equal(t, "old-tb", needs[0].ID, "card shows the new need")
	h.clean("replaced")
	// the twin lands and its dependents go on
	h.nToMerging("old-tb", "other")
	h.nLandStream("s1")
	h.must(ResolveStep(sprint.ResolveReq{}))
	for _, id := range []string{"dep1", "dep2", "dep3"} {
		assert.Equal(t, sprint.Ready, h.state(id), "%s is ready once its twin landed", id)
	}
}

// The drop and add pair already made: the replace takes over the edges of a card dropped
// before. No blocked judgment was opened, and a card already off the table keeps its reason.
func TestAddReplacesADroppedCardClosesItsBlockedJudgments(t *testing.T) {
	t.Parallel()
	h := twins(t)
	// seeded off the table, and not resolved: the edges still name old, so the replace
	// re-points them. An already-unplaced card is not dropped again.
	seedDroppedNeedWhy(h, "old", "re-cut")
	require.Empty(t, h.nOpenOf(sprint.NBlocked, ""))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"old-tb"}, Replaces: []string{"old"}, Who: "tester"}))
	assert.Empty(t, h.nOpenOf(sprint.NBlocked, ""), "the replace opens no blocked judgment")
	s := h.snap()
	assert.Equal(t, "re-cut", h.record("old").F("reason"), "a card dropped before keeps its reason")
	assert.Equal(t, "old-tb,other", s.Work.Card("dep3").F("needs"))
	h.clean("replaced after the drop")
}

func TestAddReplacesIsRefusedWholeWhenItCannotHold(t *testing.T) {
	t.Parallel()
	h := twins(t)
	for _, tc := range []struct {
		name string
		req  sprint.AddReq
		why  string
	}{
		{"two cards", sprint.AddReq{Stream: "s1", IDs: []string{"a", "b"}, Replaces: []string{"old"}}, "one card"},
		{"no such card", sprint.AddReq{Stream: "s1", IDs: []string{"a"}, Replaces: []string{"ghost"}}, "no card ghost"},
		{"itself", sprint.AddReq{Stream: "s1", IDs: []string{"old"}, Replaces: []string{"old"}}, "itself"},
		{"a sentinel", sprint.AddReq{Stream: "s1", IDs: []string{"g"}, Sentinel: true, Replaces: []string{"old"}}, "sentinel"},
		{"a cycle", sprint.AddReq{Stream: "s1", IDs: []string{"a"}, Needs: []string{"dep1"}, Replaces: []string{"old"}}, "cycle"},
	} {
		res := h.run(AddStep(tc.req))
		require.NotEmpty(t, res.Refused, tc.name)
		assert.Contains(t, res.Refused[0].Why, tc.why, tc.name)
		assert.Empty(t, res.Moved, "%s: nothing is written", tc.name)
	}
	s := h.snap()
	assert.True(t, s.Work.Card("old").Placed(), "old is where it was")
	assert.Equal(t, "old", s.Work.Card("dep1").F("needs"))
	h.clean("refused")
}

func TestRelinkRepairsTheEdgesOfADropAndAnAdd(t *testing.T) {
	t.Parallel()
	h := twins(t)
	// seeded off the table, and not resolved: the edges still name old
	seedDroppedNeedWhy(h, "old", "re-cut")
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"old-tb"}}))
	require.Empty(t, h.nOpenOf(sprint.NBlocked, ""))
	res := h.must(RelinkStep(sprint.RelinkReq{Old: []string{"old"}, New: "old-tb", Who: "tester"}))
	assert.Len(t, res.Moved, 3+1, "three dependents and the twin's weight: %v", res.Moved)
	s := h.snap()
	assert.Equal(t, "old-tb", s.Work.Card("dep1").F("needs"))
	assert.Equal(t, "old-tb,other", s.Work.Card("dep3").F("needs"))
	assert.Empty(t, h.nOpenOf(sprint.NBlocked, ""), "relink opens no blocked judgment")
	assert.Equal(t, "3", s.Work.Card("old-tb").F(sprint.FieldBehind))
	h.clean("relinked")
	// once more: nothing needs old now
	again := h.run(RelinkStep(sprint.RelinkReq{Old: []string{"old"}, New: "old-tb", Who: "tester"}))
	require.NotEmpty(t, again.Refused)
	assert.Contains(t, again.Refused[0].Why, "nothing waits on old")
}

func TestRelinkIsTheCoordinatorsAndRefusesWhatCannotHold(t *testing.T) {
	t.Parallel()
	h := twins(t)
	// seeded off the table, and not resolved: the edges still name old, so the
	// cycle is the relink's (old-tb needs dep2, and dep2 needs old)
	seedDroppedNeedWhy(h, "old", "re-cut")
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"old-tb"}, Needs: []string{"dep2"}}))
	for _, tc := range []struct {
		name string
		req  sprint.RelinkReq
		why  string
	}{
		{"not the coordinator", sprint.RelinkReq{Old: []string{"old"}, New: "other", Who: "someone"}, "coordinator"},
		{"no such twin", sprint.RelinkReq{Old: []string{"old"}, New: "ghost", Who: "tester"}, "ghost is not on the table"},
		{"a cycle", sprint.RelinkReq{Old: []string{"old"}, New: "old-tb", Who: "tester"}, "cycle"},
		{"itself", sprint.RelinkReq{Old: []string{"other"}, New: "other", Who: "tester"}, "itself"},
	} {
		res := h.run(RelinkStep(tc.req))
		require.NotEmpty(t, res.Refused, tc.name)
		assert.Contains(t, res.Refused[0].Why, tc.why, tc.name)
		assert.Empty(t, res.Moved, tc.name)
	}
	assert.Empty(t, h.nOpenOf(sprint.NBlocked, ""), "nothing was opened")
	h.clean("refused")
}

// decidedWith says a decided note's answer carries the words.
func (h *harness) decidedWith(words string) bool {
	h.t.Helper()
	all, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	for _, n := range all {
		if n.Kind == sprint.Decided && strings.Contains(n.What, words) {
			return true
		}
	}
	return false
}

// record is a work card's record, placed or not.
func (h *harness) record(id string) *sprint.Card {
	h.t.Helper()
	s, err := h.st.Load(h.ctx, All, func(*sprint.Snapshot) map[string][]string { return map[string][]string{sprint.Work: {id}} })
	require.NoError(h.t, err)
	return s.Work.Card(id)
}
