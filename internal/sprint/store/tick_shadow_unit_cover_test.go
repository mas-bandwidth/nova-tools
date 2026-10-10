package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreTickCoverShadowPlansRunning: a RUNNING machine's shadow tick plans
// the tick (Store.ShadowTick, tick.go:1963) and writes nothing. The plan names
// the epoch and state it read, carries at least one part, and its size is the
// sum of its parts' sizes; the snapshot is byte for byte what it was.
func TestStoreTickCoverShadowPlansRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	_, _, _, err := h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	before, err := h.m.Snapshot()
	require.NoError(t, err)

	plan, err := h.st.ShadowTick(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), plan.Epoch)
	assert.Equal(t, Running, plan.State)
	require.NotEmpty(t, plan.Parts, "the shadow planned no part")
	assert.Positive(t, plan.Size, "the shadow planned a positive size")
	sum := 0
	for _, p := range plan.Parts {
		sum += p.Size
	}
	assert.Equal(t, sum, plan.Size, "the size is the sum of the parts' sizes")
	assert.GreaterOrEqual(t, plan.Took, time.Duration(0))

	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a shadow tick writes nothing")
}

// TestStoreTickCoverShadowStoppedWritesNothing: the same sprint with the machine
// never started reads STOPPED and still writes nothing.
func TestStoreTickCoverShadowStoppedWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	before, err := h.m.Snapshot()
	require.NoError(t, err)

	plan, err := h.st.ShadowTick(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, Stopped, plan.State)
	assert.GreaterOrEqual(t, plan.Took, time.Duration(0))

	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a STOPPED shadow tick writes nothing")
}

// TestStoreTickCoverShadowRefusesOwedClear: a clear that advanced the epoch and
// still owes its restore is refused, naming the debt, and nothing is written
// (the shadow does not perform the restore).
func TestStoreTickCoverShadowRefusesOwedClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	ok, err := h.m.AdvanceEpoch(h.ctx, 0, t0)
	require.NoError(t, err)
	require.True(t, ok, "the epoch advanced")
	before, err := h.m.Snapshot()
	require.NoError(t, err)

	_, err = h.st.ShadowTick(h.ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still owes the restore")

	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused shadow tick writes nothing")
}

// TestStoreTickCoverShadowBusyRefusal: an operation left pending in the fence
// past the store's attempts is refused as busy, with no repair: the pending
// operation is still there and nothing is written.
func TestStoreTickCoverShadowBusyRefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.pendingOp(OpRecord{ID: "shadow-busy", Verb: "test", At: h.st.Now()})
	before, err := h.m.Snapshot()
	require.NoError(t, err)

	st := *h.st
	st.Attempts = 2
	plan, err := st.ShadowTick(h.ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the sprint is busy")
	assert.Contains(t, err.Error(), "repairs nothing")
	assert.Zero(t, plan.Size)

	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a busy shadow tick writes nothing")
	require.NotNil(t, h.m.Pending(), "the shadow repaired the pending operation")
	assert.Equal(t, "shadow-busy", h.m.Pending().ID)
}

// TestStoreTickCoverShadowPlanSize: planSize counts a plan's units, notes, rows,
// closes and updates; an empty plan is nothing.
func TestStoreTickCoverShadowPlanSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		plan sprint.Plan
		want int
	}{
		{"empty", sprint.Plan{}, 0},
		{"one of each", sprint.Plan{
			Units:   []sprint.Unit{{}},
			Notes:   []sprint.Note{{}},
			Rows:    []sprint.RowAdd{{}},
			Closes:  []sprint.Open{{}},
			Updates: []sprint.Note{{}},
		}, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, planSize(c.plan))
		})
	}
}
