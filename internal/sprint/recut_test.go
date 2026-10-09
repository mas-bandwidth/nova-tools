package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recut <card> --tier <t> | --brief-file <f> (docs/SPEC-SPRINT.md section 2, "A card
// replaced by its twin"; the coordinator, 2026-10-04: re-cutting a card for another tier or
// a smaller scope was a drop and an add by hand): the twin is add --replaces of the old
// card with the tier or the brief changed, so the dependents follow it, the old card is
// dropped "replaced by <new>" and no blocked judgment is raised; the twin records the id it
// replaces, and a twin re-cut again takes the next letter.

// recutWorld is old and other in s1, dep1 needing old and dep2 needing old and other in s2.
func recutWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"old", "other"}, Brief: proBrief, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep1"}, Needs: []string{"old"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep2"}, Needs: []string{"old", "other"}, Who: "coordinator"}))
	w.clean("recut world")
	return w
}

func TestRecutKeepsIdLineageViaReplaces(t *testing.T) {
	t.Parallel()
	w := recutWorld(t)
	before := w.s.Work.Placed("old")
	require.NotNil(t, before)
	w.must(Recut(w.s, RecutReq{ID: "old", Tier: "heavy", Who: "coordinator"}))
	w.clean("recut for a tier")
	tw := w.s.Work.Placed("oldb")
	require.NotNil(t, tw, "the twin takes the next letter")
	assert.Equal(t, "s1", tw.Row, "the twin is in its old card's stream")
	assert.Equal(t, Ready, tw.Col)
	assert.Less(t, tw.Score, before.Score, "the twin stands where its old card stood, in front of it")
	assert.Equal(t, "heavy", tw.F(FieldTier), "the twin is pinned to the tier named")
	assert.Equal(t, proBrief, tw.F("brief"), "the brief is kept when none is named")
	assert.Equal(t, "old", tw.F(FieldReplaces), "the twin records what it replaces")
	assert.Equal(t, "0", tw.F("attempt"), "the twin starts from its first attempt")
	old := w.s.Work.Card("old")
	assert.False(t, old.Placed(), "the old card is off the table")
	assert.Equal(t, "dropped", old.F("outcome"))
	assert.Equal(t, "replaced by oldb", old.F("reason"), "the old card records its twin")
	assert.Equal(t, "oldb", w.s.Work.Card("dep1").F("needs"), "a dependent needs the twin")
	assert.Equal(t, "oldb,other", w.s.Work.Card("dep2").F("needs"), "in the same place, its other need kept")
	assert.Empty(t, w.notesOf(NBlocked), "no blocked judgment is raised for a re-cut card")

	// re-cut again for a smaller scope: the next letter, the lineage and the pinned tier kept
	w.must(Recut(w.s, RecutReq{ID: "oldb", Brief: "tier: flash\nthe smaller scope", Who: "coordinator"}))
	w.clean("recut for a scope")
	tc := w.s.Work.Placed("oldc")
	require.NotNil(t, tc, "a twin re-cut takes the next letter of its lineage")
	assert.Equal(t, "oldb", tc.F(FieldReplaces))
	assert.Equal(t, "tier: flash\nthe smaller scope", tc.F("brief"))
	assert.Equal(t, "heavy", tc.F(FieldTier), "a pinned tier is kept when no tier is named")
	assert.Equal(t, "replaced by oldc", w.s.Work.Card("oldb").F("reason"))
	assert.Equal(t, "oldc,other", w.s.Work.Card("dep2").F("needs"))

	// a twin id named
	w.must(Recut(w.s, RecutReq{ID: "oldc", New: "old-small", Tier: "pro", Who: "coordinator"}))
	assert.Equal(t, "oldc", w.s.Work.Placed("old-small").F(FieldReplaces))
	assert.Equal(t, "old-small", w.s.Work.Card("dep1").F("needs"))
	assert.Empty(t, w.notesOf(NBlocked))
}

func TestRecutIsRefusedWholeWhenItChangesNothingOrCannotHold(t *testing.T) {
	t.Parallel()
	w := recutWorld(t)
	w.must(Recut(w.s, RecutReq{ID: "other", Tier: "pro", Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s3", IDs: []string{"done"}, Who: "coordinator"}))
	w.place(w.s.Work, "done", "s3", Landed)
	for _, tc := range []struct {
		name string
		req  RecutReq
		why  string
	}{
		{"nothing named", RecutReq{ID: "old"}, "wants --tier"},
		{"no such card", RecutReq{ID: "ghost", Tier: "pro"}, "no primary ghost"},
		{"a sentinel", RecutReq{ID: "stop", Tier: "pro"}, "sentinel"},
		{"landed", RecutReq{ID: "done", Tier: "pro"}, "landed"},
		{"no tier", RecutReq{ID: "old", Tier: "huge"}, "--tier wants"},
		{"the same tier", RecutReq{ID: "otherb", Tier: "pro"}, "pinned to tier pro already"},
		{"a twin id taken", RecutReq{ID: "old", New: "dep1", Tier: "pro"}, "exists already"},
	} {
		p := Recut(w.s, tc.req)
		require.NotEmpty(t, p.Refused, tc.name)
		assert.Contains(t, p.Refused[0].Why, tc.why, tc.name)
		assert.Empty(t, p.Units, "%s: nothing is written", tc.name)
	}
	assert.True(t, w.s.Work.Card("old").Placed(), "old is where it was")
	assert.Equal(t, "old", w.s.Work.Card("dep1").F("needs"))
}

// A recut preserves FIX producer urgency unless its new brief supplies a priority
// (SPEC-SPRINT, Priority); the inherited urgency must reach the actual next take.
func TestRecutPreservesFixProducerUrgencyThroughTake(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tier, brief, level, urgency, take string
	}{
		{"tier keeps urgency", "heavy", "", PriorityFix, PriorityHigh, "s1-2b.w1"},
		{"brief keeps urgency", "", friendBrief("only friend amy"), PriorityFix, PriorityHigh, "s1-2b.w1"},
		{"ordinary override replaces role", "", "tier: flash\nPRIORITY: low\nWHO: only friend amy\n\nThe task.", PriorityLow, PriorityLow, "s1-1.w1"},
		{"fix override starts normal urgency", "", "tier: flash\nPRIORITY: fix\nWHO: only friend amy\n\nThe task.", PriorityFix, PriorityNormal, "s1-1.w1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityFix, Reason: "earlier repair"}))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityHigh, Reason: "urgent producer"}))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityFix, Reason: "urgent repair"}))
			w.must(Recut(w.s, RecutReq{ID: "s1-2", Tier: tc.tier, Brief: tc.brief}))
			twin := w.s.Work.Placed("s1-2b")
			require.NotNil(t, twin)
			assert.Equal(t, tc.level, twin.F(FieldPriority))
			assert.Equal(t, tc.urgency, ProducerPriority(twin))
			seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro,heavy"}
			priorityDealWith(w, seat)
			consumer := w.s.Fleet.Card("s1-2b.w1")
			require.NotNil(t, consumer)
			assert.Equal(t, tc.level, QueuePriority(consumer))
			assert.Equal(t, tc.urgency, ProducerPriority(consumer))
			p := Take(w.s, TakeReq{As: FriendRow(seat.Name)})
			require.Len(t, p.Units, 1)
			assert.Equal(t, tc.take, p.Units[0].Key, "producer urgency reaches admission")
		})
	}
}
