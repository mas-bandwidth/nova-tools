package store

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
// (LandedAt, each kept when it is at or after the first start and no more
// than an hour of running time before the clock, the running time the clock's
// less the machine's STOPPED time between: the window by its definition, not
// by sprint.RecentLandings), oldest first.
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
	first := m.FirstStart(es.Cleared)
	for _, at := range landed {
		if !first.IsZero() && !at.Before(first) && h.now.Sub(at)-m.StoppedBetween(at, h.now) <= sprint.RateWindow {
			want.Landings = append(want.Landings, at.Unix())
		}
	}
	slices.Sort(want.Landings)
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
	// a drop of a held card (the last behind the gate) and of one landing cards
	// would not reach: off the table, out of the count
	h.must(AddStep(sprint.AddReq{Stream: "x", Count: 1}))
	h.machine()
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"g-3", "x-1"}}, Reason: "not wanted", Who: h.st.Actor}))
	h.machine()
	require.Equal(t, 4, holds("a held card dropped").Held, "the gate, two behind it, the needy card")
	require.NotEmpty(t, holds("the streams landed").Landings, "landings in the hour")

	// STOPPED: a verb holds more cards back, and the STOPPED tick counts them
	h.stopMachine()
	h.must(AddStep(sprint.AddReq{Stream: "late", Count: 2, Held: true}))
	h.machine()
	require.Equal(t, 6, holds("held while STOPPED").Held)

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

// whereFacts is WhereFacts at the work table's revision now, and whether it
// took the where record: a read of the cards reads the work table's shape, the
// record's path reads none.
func (h *harness) whereFacts(st *Store) (WhereFacts, bool) {
	h.t.Helper()
	shapes, err := st.B.Shapes(h.ctx, []string{st.Names.Table(sprint.Work)})
	require.NoError(h.t, err)
	before := h.m.Calls["shapes"]
	f, err := st.WhereFacts(h.ctx, shapes[0].Revision)
	require.NoError(h.t, err)
	return f, h.m.Calls["shapes"] == before
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
	before, counted := h.whereFacts(st)
	require.False(t, counted, "no record: the cards")
	require.Equal(t, 5, before.Held)

	h.machine()
	rec, ok := h.whereRecord()
	require.True(t, ok)
	require.Empty(t, whereBroken(rec, h.whereOfCards()))
	after, counted := h.whereFacts(st)
	require.True(t, counted, "the record at the work table's revision")
	require.Equal(t, before.Held, after.Held)

	// a verb moves the table of a STOPPED machine: until its next tick the
	// record is a revision behind, and where reads the cards
	h.stopMachine()
	h.must(AddStep(sprint.AddReq{Stream: "g2", IDs: []string{"gate2"}, Sentinel: true, Held: true}))
	stale, counted := h.whereFacts(st)
	require.False(t, counted, "a revision behind on a STOPPED machine: the cards")
	require.Equal(t, 6, stale.Held)

	// the STOPPED tick counts it again
	h.machine()
	ticked, counted := h.whereFacts(st)
	require.True(t, counted, "the STOPPED tick kept the record")
	require.Equal(t, 6, ticked.Held)

	// a clear: the record is the epoch before's, not taken, until the new
	// epoch's first tick
	h.startMachine()
	h.machine()
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	st2, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	require.NotEqual(t, st.PinnedEpoch(), st2.PinnedEpoch())
	cleared, counted := h.whereFacts(st2)
	require.False(t, counted, "the epoch before's record is not taken")
	require.Zero(t, cleared.Held)
	h.machine()
	rec, ok = h.whereRecord()
	require.True(t, ok)
	require.Equal(t, st2.PinnedEpoch(), rec.Epoch, "the new epoch's first tick counted it")
}

// putWhere writes a where record as a loop would have.
func (h *harness) putWhere(r WhereRecord) {
	h.t.Helper()
	require.NoError(h.t, h.st.putJSON(h.ctx, keyWhere, r))
}

// The tick recounts a record of another epoch even at the work table's
// revision now: a clear's new table can stand at the revision the epoch
// before's record names, and only the epoch tells them apart.
func TestTheTickRecountsAWhereRecordOfAnotherEpoch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "g", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	h.machine() // STOPPED: counts the record
	rec, ok := h.whereRecord()
	require.True(t, ok)
	require.Equal(t, 1, rec.Held)
	h.putWhere(WhereRecord{Epoch: rec.Epoch + 1, Rev: rec.Rev, Held: 99})
	h.machine()
	again, ok := h.whereRecord()
	require.True(t, ok)
	require.Empty(t, whereBroken(again, h.whereOfCards()), "the other epoch's record is counted again")
}

// where takes a record a revision behind only from the loop that keeps it:
// the machine RUNNING, its last tick within MachineSilence and not failed, and
// the record counted at or after the revision that tick saw. A loop that ticks
// and never writes the record (a binary from before it, a count passed over)
// has its heartbeat move past the record, which is then not taken. The record
// here says 99 held; the cards say 0.
func TestWhereTakesARecordBehindOnlyFromTheLoopThatKeepsIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		running bool
		hb      func(rec, now uint64) Heartbeat
		age     time.Duration
		taken   bool
	}{
		{"the tick in flight: saw the record's revision", true, func(rec, _ uint64) Heartbeat { return Heartbeat{Revisions: [4]uint64{rec}} }, 0, true},
		{"a loop that does not keep it: saw past the record", true, func(_, now uint64) Heartbeat { return Heartbeat{Revisions: [4]uint64{now}} }, 0, false},
		{"a loop failing", true, func(rec, _ uint64) Heartbeat { return Heartbeat{Revisions: [4]uint64{rec}, Error: "the store refused"} }, 0, false},
		{"a loop gone quiet", true, func(rec, _ uint64) Heartbeat { return Heartbeat{Revisions: [4]uint64{rec}} }, MachineSilence + time.Second, false},
		{"a STOPPED machine", false, func(rec, _ uint64) Heartbeat { return Heartbeat{Revisions: [4]uint64{rec}} }, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
			if c.running {
				h.startMachine()
			}
			st, err := h.st.Pinned(h.ctx)
			require.NoError(t, err)
			at := h.whereOfCards().Rev
			h.putWhere(WhereRecord{Epoch: st.PinnedEpoch(), Rev: at, Held: 99})
			// the table moves past the record: one card's field, written by
			// hand as no step of a RUNNING machine would
			c1 := h.table().Work.Card("s1-1")
			_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: st.Names.Table(sprint.Work), Epoch: "0",
				ExpectedTableRevision: fmt.Sprint(at), OperationID: "by-hand",
				Members: []ntable.BatchMemberEntry{{ID: c1.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c1.Rev)}, Set: map[string]string{"note": "moved"}}}})
			require.NoError(t, err)
			now := h.whereOfCards().Rev
			require.Greater(t, now, at)
			hb := c.hb(at, now)
			hb.At = h.now.Add(-c.age)
			require.NoError(t, h.st.putJSON(h.ctx, keyHeartbeat, hb))
			f, counted := h.whereFacts(st)
			assert.Equal(t, c.taken, counted)
			if c.taken {
				assert.Equal(t, 99, f.Held, "the record")
			} else {
				assert.Zero(t, f.Held, "the cards")
			}
		})
	}
}

// What the count adds to a tick, in round trips (a requirement is a number in
// the gate): an idle tick reads the work table's shape and the record, two
// exchanges, and writes nothing; a tick that moved the table catches the twin
// up and writes the record.
func TestTheWhereCountsTripsArePinned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 3)
	h.st.CheckTwin = nil // its fresh reads are the test's, not the tick's
	part := func(res TickResult) PartTime {
		t.Helper()
		for _, p := range res.Times {
			if p.Table == "" && p.Name == "where" {
				return p
			}
		}
		require.Fail(t, "no where part", res.TimesLine())
		return PartTime{}
	}
	moved := part(h.machine())
	assert.EqualValues(t, whereMovedTrips, moved.Trips, "a tick that moved the table: %d round trips", moved.Trips)
	assert.Zero(t, moved.Reads, "no table read whole")
	for i := 0; ; i++ {
		require.Less(t, i, 20, "the tick never went idle")
		raw, _, err := h.m.GetKey(h.ctx, keyWhere)
		require.NoError(t, err)
		res := h.machine()
		if !res.Idle {
			continue
		}
		idle := part(res)
		after, _, err := h.m.GetKey(h.ctx, keyWhere)
		require.NoError(t, err)
		assert.EqualValues(t, 2, idle.Trips, "an idle tick: the shape and the record")
		assert.Equal(t, raw, after, "an idle tick writes nothing")
		return
	}
}

// whereMovedTrips is the where part's round trips on a tick that moved the
// work table, as measured: the shape and the record read, the twin's catch-up
// (no table read whole), and the record written.
const whereMovedTrips = 8
