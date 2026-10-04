package store

// The tick's twin (twin.go): one read of the sprint per tick, every part
// planned on it, each part's receipts applied to it, and the store read again
// only when another writer wrote since. Every harness checks each part's twin
// against a fresh read of the same generation (checkTwin, store_test.go).

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A busy sprint is read whole once: the first tick reads the four tables,
// and every tick after reads only the records the world's writes between
// changed (each table caught up from its change stream, counted stale),
// never a table whole. Every read of the twin is checked against a fresh
// read (checkTwin).
func TestATickReadsTheSprintOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(200)
	h.startMachine()
	caught := int64(0)
	for i := 0; i < 8; i++ {
		res := h.machine()
		if res.State != Running || res.Done != "" {
			require.GreaterOrEqual(t, i, 3, "the sprint stopped at tick %d: too small to show the reads", i+1)
			break
		}
		c := res.Cost()
		want := int64(0)
		if i == 0 {
			want = int64(len(All))
		}
		require.Zero(t, c.Mismatch, "tick %d: the twin's records did not add up to the store's counts %d times: %s", i+1, c.Mismatch, res.TimesLine())
		require.Equal(t, want, c.Reads, "tick %d: %d tables read whole, want %d: %s", i+1, c.Reads, want, res.TimesLine())
		caught += c.Stale
		if i == 0 {
			require.NotEmpty(t, res.Parts, "the first tick did nothing: the sprint is not busy")
		}
		h.work("m1")
		h.work("m2")
		h.readAll()
		h.landAll("s1")
	}
	require.NotZero(t, caught, "no tick caught a table up from its change stream: the world's writes were not read")
	st := h.stats().twin.Load()
	require.NotZero(t, st, "no part planned on the twin")
}

// stats is the harness store's counters.
func (h *harness) stats() *Stats { return h.st.stats() }

// A write by another writer between two parts of a tick is caught up by the
// next part (counted stale) from the table's change stream: only the records
// it changed are read, never a table whole, and the part plans on what that
// writer wrote: the finish during the tick is queued, not lost, and the tick
// after drains it.
func TestAWriteDuringTheTickIsReadAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	// m2 finishes its cards, so the tick below asks them: the ask is a part that
	// runs after the world's write
	h.work("m2")
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	s := h.snap()
	var ids, primaries []string
	gens := map[string]int{}
	for _, c := range s.Fleet.Cell("m1", sprint.Working) {
		ids = append(ids, c.ID)
		primaries = append(primaries, c.F(sprint.PrimaryField))
		gens[c.ID] = c.Int("gen")
	}
	require.NotEmpty(t, ids, "m1 took nothing")
	// the world is another process: a store of its own, with no twin of the
	// tick's
	world := &Store{B: h.m, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	fired := false
	h.st.Updates = []sprint.TableUpdate{sprint.TickTables[0], {Table: sprint.Readers, Parts: []sprint.TickPartDef{{Name: "ask", Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		if !fired {
			fired = true
			res, err := world.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: "m1"}))
			require.NoError(t, err, "the world's finish: %v %v", err, res.Refused)
			require.Empty(t, res.Refused, "the world's finish: %v %v", err, res.Refused)
		}
		return sprint.TickAsk(s, r)
	}}}}, sprint.TickTables[2], sprint.TickTables[3]}
	res := h.machine()
	c := res.Cost()
	require.NotZero(t, c.Stale, "a finish during the tick: %d tables read whole, %d caught up, %d records read; want none whole, one or more caught up, a few records: %s", c.Reads, c.Stale, c.Rows, res.TimesLine())
	require.Zero(t, c.Reads, "a finish during the tick: %d tables read whole, %d caught up, %d records read; want none whole, one or more caught up, a few records: %s", c.Reads, c.Stale, c.Rows, res.TimesLine())
	require.LessOrEqual(t, c.Rows, int64(4*len(ids)+8), "a finish during the tick: %d tables read whole, %d caught up, %d records read; want none whole, one or more caught up, a few records: %s", c.Reads, c.Stale, c.Rows, res.TimesLine())
	h.st.Updates = nil
	h.machine()
	for _, id := range primaries {
		st := h.table().StateOf(id)
		require.Equal(t, sprint.Review, st, "%s is %s after the tick after the finish, want review", id, st)
	}
}

// The check has teeth: a planner that writes into the snapshot it was given
// (the twin's tables) leaves a twin that is not the store's state, and the
// next part's check against a fresh read refuses it.
func TestATwinThatDriftsIsCaught(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.startMachine()
	h.machine()
	h.st.Updates = []sprint.TableUpdate{
		{Table: sprint.Work, Parts: []sprint.TickPartDef{{Name: "scribble", Fn: func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
			for _, c := range s.Work.Column(sprint.Ready, sprint.Working) {
				c.Rev += 7 // the mutation: a planner that changes what it read
				break
			}
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "scribbled", Who: sprint.MachineActor, At: s.Now, What: "scribbled"}}}, 0
		}}}},
		{Table: sprint.Readers, Parts: []sprint.TickPartDef{{Name: "ask", Fn: sprint.TickAsk}}},
	}
	_, err := h.st.Tick(h.ctx)
	require.Error(t, err, "a scribbled twin was not caught: %v", err)
	require.Contains(t, err.Error(), "twin differs from a fresh read", "a scribbled twin was not caught: %v", err)
	t.Logf("caught: %v", err)
}

// A read of the twin cut short (the store failed as it read a table whole)
// leaves no table half read: the next read reads it whole, and the twin is
// the state a fresh read gives. The mutation this pins: a table put in the
// twin before its records were read stays empty at its revision for good.
func TestATwinReadCutShortLeavesNoTableHalfRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(6)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.readAll()
	failed := false
	h.m.Fail = func(point string) error {
		if point == "readset t-merge" && !failed {
			failed = true
			return errors.New("the store went away")
		}
		return nil
	}
	tw := NewTwin()
	_, _, err := h.st.twinRead(h.ctx, tw, All, tickExtras, nil)
	require.Error(t, err, "the cut read: %v (failed %v)", err, failed)
	require.True(t, failed, "the cut read: %v (failed %v)", err, failed)
	h.m.Fail = nil
	snap, gen, err := h.st.twinRead(h.ctx, tw, All, tickExtras, nil)
	require.NoError(t, err)
	fresh, at, err := h.st.Fenced(h.ctx, All, tickExtras, nil)
	require.NoError(t, err, "fresh read: %v at %d, twin at %d", err, at, gen.Gen)
	require.Equal(t, gen.Gen, at, "fresh read: %v at %d, twin at %d", err, at, gen.Gen)
	d := TwinDiff(snap, fresh)
	require.Empty(t, d, "the twin after a cut read: %s", d)
	require.NotEmpty(t, snap.Merge.Cards(), "the merge table is empty: the check shows nothing")
}

// The exchanges of one writing part of the tick, by kind, after another
// writer changed a table: the fence read twice (with the view, and last), the
// four shapes once, the open judgments and the coordinator once each, the
// changed table caught up from its change stream and its changed records read
// in one read set, the work table's queue once (the world's take queued its
// change, which this part plans on), then one acquire, one apply for the one
// table written and one release. No whole-table read (no cells), and no read of the machine's
// state or of the stuck record of its own: both come with the fence (the
// fold; a part that is passed over makes no exchange at all).
func TestAWritingPartsExchangesArePinned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.st.CheckTwin = nil // the check's own fresh reads are not the part's
	epoch := h.st.PinnedEpoch()
	tw := NewTwin()
	part := func(name string, fn sprint.TickPartFn) Step {
		s := TickPartStep(name, fn, sprint.TickReq{Who: sprint.MachineActor}, &epoch, nil, nil)
		s.Twin, s.Halts = tw, true
		return s
	}
	h.must(part("deal", sprint.TickDeal)) // the twin's first read: whole
	world := &Store{B: h.m, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	res, err := world.Run(h.ctx, TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	require.NoError(t, err, "the world's take: %v %+v", err, res)
	require.NotEmpty(t, res.Moved, "the world's take: %v %+v", err, res)
	before := map[string]int{}
	for k, v := range h.m.Calls {
		before[k] = v
	}
	res = h.must(part("ping", ping(sprint.Fleet, 1)))
	require.NotEmpty(t, res.Moved, "the part wrote nothing: %+v", res)
	got := map[string]int{}
	for k, v := range h.m.Calls {
		if d := v - before[k]; d != 0 {
			got[k] = d
		}
	}
	want := map[string]int{"fence": 2, "shapes": 1, "open": 1, "coord": 1, "changes": 1, "readset": 1, "queue": 1, "acquire": 1, "apply": 1, "release": 1}
	require.Equal(t, fmt.Sprint(want), fmt.Sprint(got), "a writing part's exchanges: %v, want %v", got, want)
}

// A tick's parts read the machine's state and the stuck record with their
// fence, not by reads of their own before each part: the round trips of each
// part a busy tick runs are pinned (in this store's exchanges; a read of the
// machine's state before a part, as the tick made before the fold, is one
// more in each, and the test fails).
func TestATicksPartsTripsArePinned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(40)
	h.startMachine()
	h.st.CheckTwin = nil
	h.machine()
	h.machine()
	h.work("m1")
	h.work("m2")
	busy := h.machine()
	got := map[string]int64{}
	for _, p := range busy.Times {
		if p.Table != "" {
			got[p.Table+"/"+p.Name] = p.Trips
		}
	}
	// each part: its read of the twin (the view, the fence last, the queue),
	// its acquire, one apply for each table it writes, its release; the ask
	// also catches the readers table up from its change stream; the readers'
	// beats and holds are read once by the tick, before its parts (first read)
	want := map[string]int64{"work/drain": 8, "readers/ask": 9}
	require.Equal(t, fmt.Sprint(want), fmt.Sprint(got), "the busy tick's parts made %v round trips, want %v: %s", got, want, busy.TimesLine())
}

// A pending operation whose later manifest needs lifecycle rejudgment is
// repaired against the twin's incremental work read, preserving newer state.
// This distinguishes repair reads from a genuine change-stream invalidation.
func TestTwinRepairKeepsTheWorkReadIncremental(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{{"live writer", 0}, {"past grace", time.Hour}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(2)
			h.st.CheckTwin = nil
			h.st.stats() // pinned reads share the observer counters
			tw := h.st.twin()
			pinned, err := h.st.pin(h.ctx)
			require.NoError(t, err)
			_, _, err = pinned.twinRead(h.ctx, tw, All, nil, nil)
			require.NoError(t, err)
			world := &Store{B: h.outsideWrite("s1-1"), Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
			_, err = world.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
			var cut *CutError
			require.ErrorAs(t, err, &cut)
			h.tick(tc.age)
			before := h.st.meter()
			var repaired []string
			_, _, err = pinned.twinRead(h.ctx, tw, All, nil, &repaired)
			require.NoError(t, err)
			cost := before.part("", "first read")
			require.Zero(t, cost.Reads, "pending repair read work whole: %+v, repaired %v", cost, repaired)
			require.Zero(t, cost.Mismatch)
			require.NotEmpty(t, repaired)
			require.Nil(t, h.m.Pending())
			s := h.snap()
			require.Equal(t, sprint.Ready, s.Work.Card("s1-1").Col)
			require.Equal(t, "outside", s.Work.Card("s1-1").F("brief"))
			require.Equal(t, sprint.Working, s.Work.Card("s1-2").Col)
			require.Len(t, h.skipNotes(), 1)
		})
	}
}
