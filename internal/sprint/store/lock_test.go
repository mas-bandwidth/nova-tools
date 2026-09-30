package store

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// loseFirstAcquire is the in-memory store with another writer that commits
// just before the first acquire of a tick's part: that try is lost.
type loseFirstAcquire struct {
	*Mem
	h        *harness
	acquired *[]string
}

func (s loseFirstAcquire) AtEpoch(epoch uint64, old bool) Backend {
	return loseFirstAcquire{Mem: s.Mem.AtEpoch(epoch, old).(*Mem), h: s.h, acquired: s.acquired}
}

func (s loseFirstAcquire) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if strings.HasPrefix(op.Verb, "tick ") {
		*s.acquired = append(*s.acquired, op.Verb)
		if len(*s.acquired) == 1 {
			other := &Store{B: s.Mem, Names: s.h.st.Names, Actor: "coordinator", Now: s.h.st.Now, NewID: s.h.st.NewID, Sleep: s.h.st.Sleep}
			if _, err := other.Run(ctx, Step{Verb: "poke", Plan: func(sn *sprint.Snapshot) sprint.Plan {
				return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "poked", Who: "coordinator", At: sn.Now, What: "poked"}}}
			}}); err != nil {
				return false, err
			}
		}
	}
	return s.Mem.Acquire(ctx, gen, op)
}

// A part of the tick that loses its try to another writer takes the fence
// before its next read (the lock), plans and writes under it, and the write
// cannot be lost: two tries, the second behind one lock handed over to the
// operation, and the part's moves applied.
func TestAPartThatLostATryLocksAndWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.st.CheckTwin = nil
	var acquired []string
	epoch := h.st.PinnedEpoch()
	step := TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Who: sprint.MachineActor}, &epoch, nil, nil)
	step.Twin, step.Halts, step.Pump = NewTwin(), true, true // the pump's part
	st := &Store{B: loseFirstAcquire{Mem: h.m, h: h, acquired: &acquired}, Names: h.st.Names, Actor: sprint.MachineActor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep, LockAfterLoss: true}
	relocks := h.m.Calls["relock"]
	res, err := st.Run(h.ctx, step)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tick deal", "tick deal lock"}; !slices.Equal(acquired, want) {
		t.Fatalf("the part's acquires: %v, want %v (one lost try, then the lock)", acquired, want)
	}
	if res.Attempts != 2 || h.m.Calls["relock"]-relocks != 1 || len(res.Moved) == 0 || res.Lost {
		t.Fatalf("the part after its lost try: %d attempts, %d relocks, %d moved, lost %v", res.Attempts, h.m.Calls["relock"]-relocks, len(res.Moved), res.Lost)
	}
	if f, _ := h.m.ReadFence(h.ctx); f.Pending != nil {
		t.Fatalf("the fence still holds %s", f.Pending.ID)
	}
	if st := h.table().StateOf("s1-1"); st != sprint.Working {
		t.Fatalf("s1-1 is %s after the deal under the lock, want working", st)
	}
	h.clean("locked")
}

// A part that lost a try and then, on its next read, found nothing to write
// (another writer did it) leaves the twin as it read it: the next part reads
// no table whole. (The drive's six whole-table reads after its first tick: a
// step that lost a try and ended without writing threw its twin away.)
func TestALostTryThatEndsWithoutWritingKeepsTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.st.CheckTwin = nil
	var acquired []string
	epoch := h.st.PinnedEpoch()
	tw := NewTwin()
	st := &Store{B: loseFirstAcquire{Mem: h.m, h: h, acquired: &acquired}, Names: h.st.Names, Actor: sprint.MachineActor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	plans := 0
	once := func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		plans++
		if plans > 1 {
			return sprint.Plan{}, 0 // the next read finds nothing to do
		}
		return ping(sprint.Fleet, 1)(s, r)
	}
	step := TickPartStep("once", once, sprint.TickReq{Who: sprint.MachineActor}, &epoch, nil, nil)
	step.Twin, step.Halts = tw, true
	if _, err := st.Run(h.ctx, step); err != nil {
		t.Fatal(err)
	}
	if len(acquired) != 1 {
		t.Fatalf("the part's acquires: %v, want the one lost", acquired)
	}
	next := TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Who: sprint.MachineActor}, &epoch, nil, nil)
	next.Twin, next.Halts, next.Pump = tw, true, true
	before := st.stats().reads.Load()
	if _, err := st.Run(h.ctx, next); err != nil {
		t.Fatal(err)
	}
	if n := st.stats().reads.Load() - before; n != 0 {
		t.Fatalf("the next part read %d tables whole: the twin was thrown away after a try that wrote nothing", n)
	}
}
