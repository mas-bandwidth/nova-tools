package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The twelve step builders here (RedoStep, LandedStep, MergeWindowStep,
// CostReconcileStep, FriendGiveStep, FriendLevelStep, NoteStep,
// FriendSyncStateStep, PriorityStep, RecutStep, FriendReturnStep and
// ServerRestartStep) each return a Step whose Extras and Plan closures no unit
// test reached: the unit tier's per-function coverage table named every one at
// 0.0%. These tests build each Step, assert the shape the command line reads
// (Verb, Load, Named, Mirrors, Routes and Friends), call its Extras on the
// snapshot, and run its Plan through the store's own STOPPED Mem seam: no
// sleep, no real time, no network, no Redis and no Postgres. The plans' rules
// belong to internal/sprint; these tests pin only that the builder wires them.

// --------------------------------------------------------------------------- RedoStep

// TestStoreStepsCoverRedoStep covers RedoStep: the builder names the four
// tables and runs sprint.Redo, which refuses a stream that is not on the merge
// table and moves nothing.
func TestStoreStepsCoverRedoStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	step := RedoStep(sprint.RedoReq{Sel: sprint.Sel{Stream: "s9"}})
	assert.Equal(t, "redo", step.Verb, "RedoStep sets the verb")
	assert.False(t, step.Named, "RedoStep names no card without ids")
	assert.True(t, step.Mirrors, "RedoStep mirrors display cells")
	assert.True(t, step.Routes, "RedoStep rides the routes")
	assert.True(t, step.Friends, "RedoStep consults the friends roster")
	assert.Equal(t, tables(sprint.Work, sprint.Readers, sprint.Fleet, sprint.Merge), step.Load, "RedoStep loads work, readers, fleet and merge")

	res := h.run(step)
	require.NotEmpty(t, res.Refused, "RedoStep refuses a stream that is not on the table")
	assert.Contains(t, res.Refused[0].Why, "no such stream", "the refusal names the missing stream")
	assert.Empty(t, res.Moved, "a refused redo moved nothing")
}

// --------------------------------------------------------------------------- LandedStep

// TestStoreStepsCoverLandedStep covers LandedStep: its Extras names the pins on
// the work table, and its plan is refused without a reason and, with one, for a
// card that is not on the table.
func TestStoreStepsCoverLandedStep(t *testing.T) {
	t.Parallel()

	t.Run("extras name the pins on the work table", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := LandedStep(sprint.LandedReq{Pins: []sprint.LandedPin{{ID: "s1-1", Head: "aaaa"}, {ID: "s1-2", Head: "bbbb"}}})
		assert.Equal(t, "landed", step.Verb, "LandedStep sets the verb")
		assert.True(t, step.Named, "LandedStep names its cards")
		assert.True(t, step.Mirrors, "LandedStep mirrors display cells")
		assert.Equal(t, tables(sprint.Merge, sprint.Work), step.Load, "LandedStep loads merge and work")
		assert.Equal(t, map[string][]string{sprint.Work: {"s1-1", "s1-2"}}, step.Extras(h.snap()), "Extras names the pin ids on the work table")
	})

	t.Run("a record without a reason is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := LandedStep(sprint.LandedReq{Pins: []sprint.LandedPin{{ID: "s1-1", Head: "aaaa"}}})
		res := h.run(step)
		require.NotEmpty(t, res.Refused, "a record of work found on the base says how it got there")
		assert.Contains(t, res.Refused[0].Why, "--reason <text>", "the refusal names the missing reason")
		assert.Empty(t, res.Moved, "a refused landed moved nothing")
	})

	t.Run("a card that is not on the table is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := LandedStep(sprint.LandedReq{Pins: []sprint.LandedPin{{ID: "s9-9", Head: "aaaa"}}, Reason: "found on the base", Sha: "aaaa"})
		res := h.run(step)
		require.NotEmpty(t, res.Refused, "LandedStep refuses a card that is not on the table")
		assert.Contains(t, res.Refused[0].Why, "no such card", "the refusal names the missing card")
		assert.Empty(t, res.Moved, "a refused landed moved nothing")
	})
}

// --------------------------------------------------------------------------- MergeWindowStep

// TestStoreStepsCoverMergeWindowStep covers MergeWindowStep: the coordinator
// opens the merge window from the step's clock, and a duration of none is
// refused.
func TestStoreStepsCoverMergeWindowStep(t *testing.T) {
	t.Parallel()

	t.Run("the coordinator opens a window", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := MergeWindowStep(sprint.MergeWindowReq{For: "10m", Reason: "release in progress", Who: "tester"})
		assert.Equal(t, "merge-window open", step.Verb, "MergeWindowStep sets the verb")
		assert.False(t, step.Named, "MergeWindowStep names no card")
		assert.False(t, step.Mirrors, "MergeWindowStep mirrors no display cell")
		assert.Equal(t, tables(sprint.Merge), step.Load, "MergeWindowStep loads the merge table")

		res := h.must(step)
		require.NotEmpty(t, res.Moved, "the open window is said")
		assert.Contains(t, res.Moved[0], "merge window open until", "the result names the pause")
		assert.Contains(t, res.Moved[0], "release in progress", "the result names the reason")
	})

	t.Run("a window of no duration is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := MergeWindowStep(sprint.MergeWindowReq{For: "0s", Reason: "release in progress", Who: "tester"})
		res := h.run(step)
		require.NotEmpty(t, res.Refused, "a window of no duration is refused")
		assert.Contains(t, res.Refused[0].Why, "--for wants a duration above zero", "the refusal names what --for wants")
		assert.Empty(t, res.Moved, "a refused merge window moved nothing")
	})
}

// --------------------------------------------------------------------------- CostReconcileStep

// TestStoreStepsCoverCostReconcileStep covers CostReconcileStep: the builder
// names the work and fleet tables, rides the routes, and a reconciliation with
// no reads is not refused.
func TestStoreStepsCoverCostReconcileStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	step := CostReconcileStep(sprint.CostReconcileReq{Who: "tester"})
	assert.Equal(t, "cost reconcile", step.Verb, "CostReconcileStep sets the verb")
	assert.True(t, step.Routes, "CostReconcileStep rides the routes")
	assert.Equal(t, tables(sprint.Work, sprint.Fleet), step.Load, "CostReconcileStep loads work and fleet")

	res := h.run(step)
	assert.Empty(t, res.Refused, "a reconciliation with no reads is not refused")
}

// --------------------------------------------------------------------------- FriendGiveStep

// TestStoreStepsCoverFriendGiveStep covers FriendGiveStep: the builder names the
// fleet and work tables, mirrors display cells, and a card that is not on either
// table is refused.
func TestStoreStepsCoverFriendGiveStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	step := FriendGiveStep(sprint.FriendGiveReq{Friend: "amy", IDs: []string{"s9-9"}})
	assert.Equal(t, "friend give", step.Verb, "FriendGiveStep sets the verb")
	assert.False(t, step.Named, "FriendGiveStep names no card")
	assert.True(t, step.Mirrors, "FriendGiveStep mirrors display cells")
	assert.Equal(t, tables(sprint.Fleet, sprint.Work), step.Load, "FriendGiveStep loads fleet and work")

	res := h.run(step)
	require.NotEmpty(t, res.Refused, "a card that is not on the table is refused")
	assert.Contains(t, res.Refused[0].Why, "no card", "the refusal names the missing card")
	assert.Empty(t, res.Moved, "a refused friend give moved nothing")
}

// --------------------------------------------------------------------------- FriendLevelStep

// TestStoreStepsCoverFriendLevelStep covers FriendLevelStep: the builder names
// the fleet and work tables, mirrors display cells, and a level with no seats
// runs with no refusal.
func TestStoreStepsCoverFriendLevelStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	step := FriendLevelStep(sprint.FriendLevelReq{Who: "tester"})
	assert.Equal(t, "friend level", step.Verb, "FriendLevelStep sets the verb")
	assert.True(t, step.Mirrors, "FriendLevelStep mirrors display cells")
	assert.Equal(t, tables(sprint.Fleet, sprint.Work), step.Load, "FriendLevelStep loads fleet and work")

	res := h.run(step)
	assert.Empty(t, res.Refused, "a level with no seats is not refused")
}

// --------------------------------------------------------------------------- NoteStep

// TestStoreStepsCoverNoteStep covers NoteStep: its Plan stamps the note with the
// snapshot's clock and plans that note alone.
func TestStoreStepsCoverNoteStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	n := sprint.Note{ID: "n1", Kind: "happened", Type: "test note", Stream: "s1"}
	step := NoteStep("friend sync", n)
	assert.Equal(t, "friend sync", step.Verb, "NoteStep sets the verb it is given")
	assert.Equal(t, ArgsOf(n), step.Args, "NoteStep carries the note's arguments")

	snap := h.snap()
	plan := step.Plan(snap)
	require.Len(t, plan.Notes, 1, "NoteStep plans its one note")
	assert.Equal(t, "n1", plan.Notes[0].ID, "the plan holds the note it was given")
	assert.True(t, plan.Notes[0].At.Equal(snap.Now), "the note is stamped with the snapshot's clock")
	assert.Empty(t, plan.Units, "NoteStep plans that note alone")
	assert.Empty(t, plan.Props, "NoteStep plans that note alone")
	assert.Empty(t, plan.Refused, "NoteStep plans that note alone")
}

// --------------------------------------------------------------------------- FriendSyncStateStep

// TestStoreStepsCoverFriendSyncStateStep covers FriendSyncStateStep: an ok pass
// with nothing recorded plans nothing, and a failing pass writes the fleet
// property.
func TestStoreStepsCoverFriendSyncStateStep(t *testing.T) {
	t.Parallel()

	t.Run("an ok pass with nothing recorded plans nothing", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := FriendSyncStateStep(sprint.FriendSyncStateReq{Who: "tester"})
		assert.Equal(t, "friend sync", step.Verb, "FriendSyncStateStep sets the verb")
		assert.Equal(t, tables(sprint.Fleet), step.Load, "FriendSyncStateStep loads the fleet table")

		plan := step.Plan(h.snap())
		assert.Empty(t, plan.Props, "nothing recorded and not failing plans no write")
		assert.Empty(t, plan.Notes, "nothing recorded and not failing plans no note")
		assert.Empty(t, plan.Units, "nothing recorded and not failing plans no unit")
		assert.Empty(t, plan.Refused, "nothing recorded and not failing is not refused")
	})

	t.Run("a failing pass writes the fleet property", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := FriendSyncStateStep(sprint.FriendSyncStateReq{Failing: true, Since: t0, Failed: 3, Exit: 1, Said: "schema config is at version 35", Who: "tester"})
		plan := step.Plan(h.snap())
		require.Len(t, plan.Props, 1, "the failing state is written")
		assert.Equal(t, sprint.Fleet, plan.Props[0].Table, "the state is a fleet property")
		assert.Equal(t, sprint.PropFriendSync, plan.Props[0].Name, "the state is written under the friend sync property")
		assert.NotEmpty(t, plan.Props[0].Value, "the state's JSON is written")
		assert.Empty(t, plan.Refused, "a failing pass is not refused")
	})
}

// --------------------------------------------------------------------------- PriorityStep

// TestStoreStepsCoverPriorityStep covers PriorityStep: its builder names the
// work and merge tables, a call with no card and no stream is refused, and a
// stream that is not on the table is refused.
func TestStoreStepsCoverPriorityStep(t *testing.T) {
	t.Parallel()

	t.Run("no card and no stream is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := PriorityStep(sprint.PriorityReq{Reason: "the owner asks"})
		assert.Equal(t, "priority", step.Verb, "PriorityStep sets the verb")
		assert.Equal(t, tables(sprint.Work, sprint.Merge), step.Load, "PriorityStep loads work and merge")

		res := h.run(step)
		require.NotEmpty(t, res.Refused, "priority wants a card or a stream")
		assert.Contains(t, res.Refused[0].Why, "run: nova-sprint help priority", "the refusal names the verb's help")
		assert.Empty(t, res.Moved, "a refused priority moved nothing")
	})

	t.Run("a stream that is not on the table is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := PriorityStep(sprint.PriorityReq{Stream: "s9", Level: sprint.PriorityHigh, Reason: "the owner asks"})
		res := h.run(step)
		require.NotEmpty(t, res.Refused, "PriorityStep refuses a stream that is not on the table")
		assert.Contains(t, res.Refused[0].Why, "no such stream", "the refusal names the missing stream")
		assert.Empty(t, res.Moved, "a refused priority moved nothing")
	})
}

// --------------------------------------------------------------------------- RecutStep

// TestStoreStepsCoverRecutStep covers RecutStep: its Extras for a card that is
// not on the table is the id, its needs and its new id; for a placed card it
// adds the merge table's control card; and a call with neither a tier nor a
// brief is refused.
func TestStoreStepsCoverRecutStep(t *testing.T) {
	t.Parallel()

	t.Run("extras for a card that is not on the table name it, its needs and its new id", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := RecutStep(sprint.RecutReq{ID: "s9-9", New: "s9-9b", Needs: []string{"s1-1"}})
		assert.Equal(t, "recut", step.Verb, "RecutStep sets the verb")
		assert.True(t, step.Named, "RecutStep names its card")
		assert.True(t, step.Mirrors, "RecutStep mirrors display cells")
		assert.Equal(t, All, step.Load, "RecutStep reads every table")

		extras := step.Extras(h.snap())
		assert.Equal(t, []string{"s9-9", "s1-1", "s9-9b"}, extras[sprint.Work], "Extras names the id, its needs and the twin's id")
		assert.NotContains(t, extras, sprint.Merge, "a card that is not on the table reads no control card")
	})

	t.Run("extras for a placed card add the merge control card", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)

		step := RecutStep(sprint.RecutReq{ID: "s1-1"})
		extras := step.Extras(h.snap())
		assert.Contains(t, extras[sprint.Work], "s1-1", "Extras names the card re-cut")
		require.Len(t, extras[sprint.Merge], 1, "the card's stream's control card is read")
		c := h.snap().Work.Placed("s1-1")
		require.NotNil(t, c, "the card is on the table")
		assert.Equal(t, sprint.CtlID(c.Row), extras[sprint.Merge][0], "the control card is the card's stream's")
	})

	t.Run("neither a tier nor a brief is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)

		step := RecutStep(sprint.RecutReq{ID: "s1-1"})
		res := h.run(step)
		require.NotEmpty(t, res.Refused, "a recut that changes nothing is refused")
		assert.Contains(t, res.Refused[0].Why, "recut wants --tier", "the refusal names what recut wants")
		assert.Empty(t, res.Moved, "a refused recut moved nothing")

		c := h.snap().Work.Placed("s1-1")
		require.NotNil(t, c, "the refused recut left the card where it was")
		assert.Equal(t, "s1", c.Row, "the refused recut moved no card")
	})
}

// --------------------------------------------------------------------------- FriendReturnStep

// TestStoreStepsCoverFriendReturnStep covers FriendReturnStep: its Extras names
// the work cards that are not on the fleet table, a work card that is not there
// is refused, and a card named twice is refused.
func TestStoreStepsCoverFriendReturnStep(t *testing.T) {
	t.Parallel()

	t.Run("extras name the work cards that are not on the fleet table", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := FriendReturnStep(sprint.FriendReturnReq{Friend: "amy", Cards: []sprint.FriendReturnCard{{ID: "w-9"}}})
		assert.Equal(t, "friend reconcile", step.Verb, "FriendReturnStep sets the verb")
		assert.True(t, step.Named, "FriendReturnStep names its cards")
		assert.True(t, step.Mirrors, "FriendReturnStep mirrors display cells")
		assert.Equal(t, tables(sprint.Fleet, sprint.Work), step.Load, "FriendReturnStep loads fleet and work")
		assert.Equal(t, map[string][]string{sprint.Fleet: {"w-9"}}, step.Extras(h.snap()), "Extras names the work card off the fleet table")
	})

	t.Run("a work card that is not on the fleet table is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := FriendReturnStep(sprint.FriendReturnReq{Friend: "amy", Cards: []sprint.FriendReturnCard{{ID: "w-9", Gen: 1, Why: "abandoned"}}})
		res := h.run(step)
		require.NotEmpty(t, res.Refused, "FriendReturnStep refuses a work card that is not on the fleet table")
		assert.Contains(t, res.Refused[0].Why, "no such work card", "the refusal names the missing work card")
		assert.Empty(t, res.Moved, "a refused friend return moved nothing")
	})

	t.Run("a card named twice is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		step := FriendReturnStep(sprint.FriendReturnReq{Friend: "amy", Cards: []sprint.FriendReturnCard{{ID: "w-9", Gen: 1, Why: "abandoned"}, {ID: "w-9", Gen: 1, Why: "abandoned"}}})
		res := h.run(step)
		require.Len(t, res.Refused, 2, "both the unknown card and the repeat are named")
		assert.Contains(t, res.Refused[0].Why, "no such work card", "the first naming is refused as unknown")
		assert.Contains(t, res.Refused[1].Why, "named twice", "the repeat is refused as a repeat")
		assert.Empty(t, res.Moved, "a refused friend return moved nothing")
	})
}

// --------------------------------------------------------------------------- ServerRestartStep

// TestStoreStepsCoverServerRestartStep covers ServerRestartStep: the builder
// names the readers and work tables, and a restart with nothing in flight runs
// with no refusal.
func TestStoreStepsCoverServerRestartStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	step := ServerRestartStep()
	assert.Equal(t, "restart", step.Verb, "ServerRestartStep sets the verb")
	assert.False(t, step.Named, "ServerRestartStep names no card")
	assert.False(t, step.Mirrors, "ServerRestartStep mirrors no display cell")
	assert.Equal(t, tables(sprint.Readers, sprint.Work), step.Load, "ServerRestartStep loads readers and work")

	res := h.run(step)
	assert.Empty(t, res.Refused, "a restart with nothing in flight is not refused")
}
