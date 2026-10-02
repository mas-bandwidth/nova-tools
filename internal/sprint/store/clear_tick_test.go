package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				cleared, err = h.st.Clear(h.ctx)
				assert.NoError(t, err, "clear: %v", err)
				img = withoutMachine(h.image())
			}}
			res, err := loop.Tick(h.ctx)
			require.NotEmpty(t, img, "the clear never landed in the tick: %+v %v", res, err)
			require.NoError(t, err, "the tick at a clear: stale %q parts %+v err %v", res.Stale, res.Parts, err)
			require.NotEmpty(t, res.Stale, "the tick at a clear: stale %q parts %+v err %v", res.Stale, res.Parts, err)
			got := withoutMachine(h.image())
			require.Equal(t, img, got, "the tick wrote after the clear:\n%s\nwas\n%s", got, img)
			m, _, _ := h.st.Machine(h.ctx)
			require.False(t, m.Running(), "after the clear: machine %s, %+v", m.StateWord(), cleared)
			require.Equal(t, uint64(1), cleared.To, "after the clear: machine %s, %+v", m.StateWord(), cleared)
			h.clean("after the tick")
		})
	}
}
