package store

// The tick's twin (twin.go): one read of the sprint per tick, every part
// planned on it, each part's receipts applied to it, and the store read again
// only when another writer wrote since. Every harness checks each part's twin
// against a fresh read of the same generation (checkTwin, store_test.go).

import (
	"errors"
	"strings"
	"testing"

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
			if i < 3 {
				t.Fatalf("the sprint stopped at tick %d: too small to show the reads", i+1)
			}
			break
		}
		c := res.Cost()
		want := int64(0)
		if i == 0 {
			want = int64(len(All))
		}
		if c.Reads != want {
			t.Fatalf("tick %d: %d tables read whole, want %d: %s", i+1, c.Reads, want, res.TimesLine())
		}
		caught += c.Stale
		if i == 0 && len(res.Parts) == 0 {
			t.Fatal("the first tick did nothing: the sprint is not busy")
		}
		h.work("m1")
		h.work("m2")
		h.readAll()
		h.landAll("s1")
	}
	if caught == 0 {
		t.Fatal("no tick caught a table up from its change stream: the world's writes were not read")
	}
	if st := h.stats().twin.Load(); st == 0 {
		t.Fatal("no part planned on the twin")
	}
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
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	s := h.snap()
	var ids, primaries []string
	gens := map[string]int{}
	for _, c := range s.Fleet.Cell("m1", sprint.Working) {
		ids = append(ids, c.ID)
		primaries = append(primaries, c.F(sprint.PrimaryField))
		gens[c.ID] = c.Int("gen")
	}
	if len(ids) == 0 {
		t.Fatal("m1 took nothing")
	}
	// the world is another process: a store of its own, with no twin of the
	// tick's
	world := &Store{B: h.m, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	fired := false
	h.st.Updates = []sprint.TableUpdate{sprint.TickTables[0], {Table: sprint.Readers, Parts: []sprint.TickPartDef{{Name: "ask", Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		if !fired {
			fired = true
			res, err := world.Run(h.ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: "m1"}))
			if err != nil || len(res.Refused) > 0 {
				t.Fatalf("the world's finish: %v %v", err, res.Refused)
			}
		}
		return sprint.TickAsk(s, r)
	}}}}, sprint.TickTables[2], sprint.TickTables[3]}
	res := h.machine()
	c := res.Cost()
	if c.Stale == 0 || c.Reads != 0 || c.Rows > int64(4*len(ids)+8) {
		t.Fatalf("a finish during the tick: %d tables read whole, %d caught up, %d records read; want none whole, one or more caught up, a few records: %s", c.Reads, c.Stale, c.Rows, res.TimesLine())
	}
	h.st.Updates = nil
	h.machine()
	for _, id := range primaries {
		if st := h.table().StateOf(id); st != sprint.Review {
			t.Fatalf("%s is %s after the tick after the finish, want review", id, st)
		}
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
	if err == nil || !strings.Contains(err.Error(), "twin differs from a fresh read") {
		t.Fatalf("a scribbled twin was not caught: %v", err)
	}
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
	if _, _, err := h.st.twinRead(h.ctx, tw, All, tickExtras, nil); err == nil || !failed {
		t.Fatalf("the cut read: %v (failed %v)", err, failed)
	}
	h.m.Fail = nil
	snap, gen, err := h.st.twinRead(h.ctx, tw, All, tickExtras, nil)
	if err != nil {
		t.Fatal(err)
	}
	fresh, at, err := h.st.Fenced(h.ctx, All, tickExtras, nil)
	if err != nil || at != gen.Gen {
		t.Fatalf("fresh read: %v at %d, twin at %d", err, at, gen.Gen)
	}
	if d := TwinDiff(snap, fresh); d != "" {
		t.Fatalf("the twin after a cut read: %s", d)
	}
	if len(snap.Merge.LoadedCards()) == 0 {
		t.Fatal("the merge table is empty: the check shows nothing")
	}
}
