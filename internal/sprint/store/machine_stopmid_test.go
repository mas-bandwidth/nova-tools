package store

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

type stopAtAcquire struct {
	*Mem
	h    *harness
	done bool
}

func (s *stopAtAcquire) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if !s.done && op.Verb == "tick resolve" {
		s.done = true
		other := &Store{B: s.Mem, Names: s.h.st.Names, Actor: "coordinator", Now: s.h.st.Now, NewID: s.h.st.NewID}
		if _, _, _, err := other.SetMachine(ctx, false); err != nil {
			panic(err)
		}
	}
	return s.Mem.Acquire(ctx, gen, op)
}

// stop lands while the tick's first part is being written: every later part
// of that tick still runs, after the machine said STOPPED.
func TestCRPartsRunAfterStop(t *testing.T) {
	t.Parallel()
	h := raceScene(t)
	h.startMachine()
	h.st.B = &stopAtAcquire{Mem: h.m, h: h}
	res := h.machine()
	h.st.B = h.m
	m, _, _ := h.st.Machine(h.ctx)
	var after []string
	for _, p := range res.Parts {
		if p.Name != "resolve" {
			after = append(after, p.Name)
		}
	}
	t.Logf("machine %s; parts that ran after the stop: %v; moved %d", m.StateWord(), after, len(res.Moved()))
	if !m.Running() && len(after) > 0 {
		t.Errorf("C: the machine was STOPPED (stop returned) and the tick went on to run %v", after)
	}
	_ = sprint.Work
}
