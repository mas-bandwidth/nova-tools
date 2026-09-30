package store

// The tick's twin (twin.go): one read of the sprint per tick, every part
// planned on it, each part's receipts applied to it, and the store read again
// only when another writer wrote since. Every harness checks each part's twin
// against a fresh read of the same generation (checkTwin, store_test.go).

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A tick of a busy sprint reads the store once: its first read. Every part
// after it plans on the twin (checked against a fresh read at every part),
// and nothing it read was stale, since no other writer wrote during it.
func TestATickReadsTheSprintOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(200)
	h.startMachine()
	for i := 0; i < 8; i++ {
		res := h.machine()
		if res.State != Running || res.Done != "" {
			if i < 3 {
				t.Fatalf("the sprint stopped at tick %d: too small to show the reads", i+1)
			}
			break
		}
		c := res.Cost()
		if i == 0 {
			// the first tick tells of the harness's machines that beat before
			// they were brought up: a step of its own, before the tick's read
			c.Reads -= res.Times[0].Reads
		}
		if c.Reads != 1 || c.Stale != 0 {
			t.Fatalf("tick %d: %d whole reads, %d stale; want 1 and 0: %s", i+1, c.Reads, c.Stale, res.TimesLine())
		}
		if i == 0 && len(res.Parts) == 0 {
			t.Fatal("the first tick did nothing: the sprint is not busy")
		}
		h.work("m1")
		h.work("m2")
		h.readAll()
		h.landAll("s1")
	}
	if st := h.stats().twin.Load(); st == 0 {
		t.Fatal("no part planned on the twin")
	}
}

// stats is the harness store's counters.
func (h *harness) stats() *Stats { return h.st.stats() }

// A write by another writer between two parts of a tick makes the next part
// read the store again (counted as stale), and it plans on what that writer
// wrote: the finish during the tick is queued, not lost, and the tick after
// drains it.
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
	fired := false
	h.st.Updates = []sprint.TableUpdate{sprint.TickTables[0], {Table: sprint.Readers, Parts: []sprint.TickPartDef{{Name: "ask", Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		if !fired {
			fired = true
			h.must(FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: "m1"}))
		}
		return sprint.TickAsk(s, r)
	}}}}, sprint.TickTables[2], sprint.TickTables[3]}
	res := h.machine()
	c := res.Cost()
	if c.Stale == 0 || c.Reads < 2 {
		t.Fatalf("a finish during the tick: %d whole reads, %d stale; want a second read, counted stale: %s", c.Reads, c.Stale, res.TimesLine())
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
