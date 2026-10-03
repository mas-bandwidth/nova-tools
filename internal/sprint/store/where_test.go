package store

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// whereRecord is the where record as stored; false when there is none.
func (h *harness) whereRecord() (WhereRecord, bool) {
	h.t.Helper()
	raw, ok, err := h.m.GetKey(h.ctx, keyWhere)
	require.NoError(h.t, err)
	return readWhere(raw, ok)
}

// whereOfCards is what the where record must hold, read from the cards as
// where read them before the record: the work table's revision, the held cards
// (HeldBack over the waiting column) and the landings the rate can count
// (LandedAt, windowed at the clock's reading).
func (h *harness) whereOfCards() WhereRecord {
	h.t.Helper()
	st, err := h.st.Pinned(h.ctx)
	require.NoError(h.t, err)
	shapes, err := st.B.Shapes(h.ctx, []string{st.Names.Table(sprint.Work)})
	require.NoError(h.t, err)
	held, err := st.HeldBack(h.ctx)
	require.NoError(h.t, err)
	landed, err := st.LandedAt(h.ctx)
	require.NoError(h.t, err)
	m, _, err := st.Machine(h.ctx)
	require.NoError(h.t, err)
	es, err := st.EpochNow(h.ctx)
	require.NoError(h.t, err)
	want := WhereRecord{Epoch: st.PinnedEpoch(), Rev: shapes[0].Revision, Held: held}
	for _, at := range sprint.RecentLandings(landed, m.Spans, m.FirstStart(es.Cleared), h.now) {
		want.Landings = append(want.Landings, at.Unix())
	}
	return want
}

// whereBroken is the record's invariant against the cards (the model's
// HeldIsHeldBack and LandingsInOrder, written as a check): of the epoch and
// at the work table's revision; held = the cards behind a sentinel not
// released, admitted held, or waiting on one; the landings the window can
// count, every one, oldest first. "" when it holds.
func whereBroken(got, want WhereRecord) string {
	switch {
	case got.Epoch != want.Epoch || got.Rev != want.Rev:
		return fmt.Sprintf("counted at epoch %d revision %d, the work table is at epoch %d revision %d", got.Epoch, got.Rev, want.Epoch, want.Rev)
	case got.Held != want.Held:
		return fmt.Sprintf("held %d, the cards hold back %d", got.Held, want.Held)
	case !slices.IsSorted(got.Landings):
		return fmt.Sprintf("landings out of order: %v", got.Landings)
	case !slices.Equal(got.Landings, want.Landings):
		return fmt.Sprintf("landings %v, the cards landed %v", got.Landings, want.Landings)
	}
	return ""
}

// The where record is what where shows beside the counts (docs/SPEC-SPRINT.md
// section 1): after every tick, RUNNING or STOPPED, it is the held cards and
// the hour's landings as the cards say, at the work table's revision the tick
// left, so where reads no card. Driven through sentinels held and released, a
// need on a held card (held through the need), held cards added while the
// machine is STOPPED, and every card to landed. No real time: the clock is the
// harness's.
func TestTheWhereRecordIsTheCardsAfterEveryTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 4)
	h.must(AddStep(sprint.AddReq{Stream: "g", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	h.must(AddStep(sprint.AddReq{Stream: "g", Count: 3}))
	h.must(AddStep(sprint.AddReq{Stream: "n", IDs: []string{"needy"}, Needs: []string{"g-2"}}))
	_, ok := h.whereRecord()
	require.False(t, ok, "no tick yet: no record, where counts the cards")
	holds := func(when string) WhereRecord {
		t.Helper()
		got, ok := h.whereRecord()
		require.True(t, ok, "%s: the tick wrote no record", when)
		want := h.whereOfCards()
		require.Empty(t, whereBroken(got, want), "%s", when)
		return got
	}
	members := []string{"m1", "m2"}
	for round := 0; round < 12; round++ {
		h.machine()
		rec := holds(fmt.Sprintf("round %d", round))
		require.Equal(t, 5, rec.Held, "round %d: the gate, the three behind it, and the card that needs one of them", round)
		h.play(members, "s1", "s2", "s3")
	}
	require.NotEmpty(t, holds("the streams landed").Landings, "landings in the hour")

	// STOPPED: a verb holds more cards back, and the STOPPED tick counts them
	h.stopMachine()
	h.must(AddStep(sprint.AddReq{Stream: "late", Count: 2, Held: true}))
	h.machine()
	require.Equal(t, 7, holds("held while STOPPED").Held)

	// released: the gate lands, the three behind it and the needy card go on
	h.startMachine()
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"gate"}, Reason: "the wave is loaded", Coordinator: h.st.Actor, Who: h.st.Actor}))
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"late-1", "late-2"}, Reason: "the wave is loaded", Coordinator: h.st.Actor, Who: h.st.Actor}))
	for round := 0; round < 12; round++ {
		h.machine()
		rec := holds(fmt.Sprintf("released, round %d", round))
		require.Zero(t, rec.Held, "released, round %d", round)
		h.play(members, "s1", "s2", "s3", "g", "n", "late")
	}
	h.machine()
	final := holds("every card landed")
	require.Equal(t, int(landedAll(h)), len(final.Landings), "every landing is inside the hour of running time: %v", final.Landings)

	// every card landed, the machine stopped itself: two hours STOPPED are no
	// running time, and the hour still holds every landing
	require.False(t, h.machineRecord().Running(), "done: the machine stopped itself")
	h.tick(2 * time.Hour)
	h.must(AddStep(sprint.AddReq{Stream: "after", Count: 1, Held: true}))
	h.machine()
	require.Len(t, holds("two hours STOPPED").Landings, len(final.Landings))
	// two hours RUNNING: the window has moved past them, and the count keeps none
	h.startMachine()
	h.tick(2 * time.Hour)
	h.must(AddStep(sprint.AddReq{Stream: "after", Count: 1, Held: true}))
	h.machine()
	require.Empty(t, holds("two hours running").Landings)

	// reversed witnesses: each breaks the invariant, and the check says so
	want := h.whereOfCards()
	want.Landings = []int64{100, 200, 300}
	for name, bad := range map[string]func(r *WhereRecord){
		"a held card missed":         func(r *WhereRecord) { r.Held-- },
		"a card counted twice":       func(r *WhereRecord) { r.Held++ },
		"counted before the moves":   func(r *WhereRecord) { r.Rev-- },
		"another epoch's":            func(r *WhereRecord) { r.Epoch++ },
		"the landings out of order":  func(r *WhereRecord) { slices.Reverse(r.Landings) },
		"the newest landing dropped": func(r *WhereRecord) { r.Landings = r.Landings[:2] },
	} {
		r := want
		r.Landings = slices.Clone(want.Landings)
		bad(&r)
		require.NotEmpty(t, whereBroken(r, want), "the witness %q is not caught", name)
	}
}

// landedAll is the primaries landed on the work table.
func landedAll(h *harness) int64 {
	var n int64
	s := h.table()
	for _, row := range s.Work.Rows() {
		n += int64(s.Work.Count(row, sprint.Landed))
	}
	return n
}

// The live sprint has no where record (it predates it): where reads the cards
// until the first tick counts it, and the record is the cards from then on;
// a clear leaves the record of the epoch before, which where does not take,
// until the new epoch's first tick.
func TestTheFirstTickCountsTheWhereRecordAndAClearMakesItAnew(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "g", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	h.must(AddStep(sprint.AddReq{Stream: "g", Count: 4}))
	holesUp(h, 3)
	st, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	before, err := st.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	require.False(t, before.Counted, "no record: the cards")
	require.Equal(t, 5, before.Held)

	h.machine()
	rec, ok := h.whereRecord()
	require.True(t, ok)
	require.Empty(t, whereBroken(rec, h.whereOfCards()))
	after, err := st.WhereFacts(h.ctx, rec.Rev)
	require.NoError(t, err)
	require.True(t, after.Counted, "the record at the work table's revision")
	require.Equal(t, before.Held, after.Held)

	// a verb moves the table and no loop ticks (the heartbeat is old): the
	// record is a revision behind, and where reads the cards
	h.stopMachine()
	h.tick(MachineSilence + time.Second)
	h.must(AddStep(sprint.AddReq{Stream: "g2", IDs: []string{"gate2"}, Sentinel: true, Held: true}))
	moved := h.whereOfCards()
	stale, err := st.WhereFacts(h.ctx, moved.Rev)
	require.NoError(t, err)
	require.False(t, stale.Counted, "a revision behind with no loop ticking: the cards")
	require.Equal(t, 6, stale.Held)

	// the STOPPED loop ticks: the record follows the verb, and is taken while
	// the loop ticks
	h.machine()
	ticked, err := st.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	require.True(t, ticked.Counted, "a loop that ticks keeps the record")
	require.Equal(t, 6, ticked.Held)

	// a clear: the record is the epoch before's, not taken even while the loop
	// ticks, until the new epoch's first tick
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	st2, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	require.NotEqual(t, st.PinnedEpoch(), st2.PinnedEpoch())
	cleared, err := st2.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	require.False(t, cleared.Counted, "the epoch before's record is not taken")
	require.Zero(t, cleared.Held)
	h.machine()
	rec, ok = h.whereRecord()
	require.True(t, ok)
	require.Equal(t, st2.PinnedEpoch(), rec.Epoch, "the STOPPED tick counted the new epoch")
}
