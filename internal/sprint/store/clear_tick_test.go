package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// C4: a tick in flight at a clear, wherever the clear lands in it, writes
// nothing at the new epoch (nor at the old one after the clear): it stops
// that pass, and the machine is STOPPED.
func TestATickInFlightAtAClearWritesNothing(t *testing.T) {
	t.Parallel()
	for _, at := range []struct {
		kind string
		n    int
	}{{"fence", 1}, {"shapes", 1}, {"shapes", 2}, {"fence", 3}, {"fence", 4}, {"acquire", 1}, {"apply", 1}, {"release", 1}} {
		t.Run(at.kind+"-"+itoa(uint64(at.n)), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(4)
			h.startMachine()
			var img string
			var cleared ClearResult
			loop := *h.st
			loop.Actor = sprint.MachineActor
			loop.B = &clearOnCall{Backend: h.m, kv: h.m, kind: at.kind, n: at.n, clear: func() {
				var err error
				if cleared, err = h.st.Clear(h.ctx); err != nil {
					t.Errorf("clear: %v", err)
				}
				img = withoutMachine(h.image())
			}}
			res, err := loop.Tick(h.ctx)
			if img == "" {
				t.Fatalf("the clear never landed in the tick: %+v %v", res, err)
			}
			if err != nil || res.Stale == "" {
				t.Fatalf("the tick at a clear: stale %q parts %+v err %v", res.Stale, res.Parts, err)
			}
			if got := withoutMachine(h.image()); got != img {
				t.Fatalf("the tick wrote after the clear:\n%s\nwas\n%s", got, img)
			}
			if m, _, _ := h.st.Machine(h.ctx); m.Running() || cleared.To != 1 {
				t.Fatalf("after the clear: machine %s, %+v", m.StateWord(), cleared)
			}
			h.clean("after the tick")
		})
	}
}
