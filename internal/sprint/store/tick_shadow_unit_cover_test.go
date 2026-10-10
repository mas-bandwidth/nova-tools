package store

// The unit cover of the shadow tick's plan and its fenced read (a reader's
// finding: Store.ShadowTick, Store.shadowRead and planSize of tick.go, whose
// only test lived in another package, cmd/nova-sprint, so none of it counted
// for the store). The whole plan and its one fenced read run on the package's
// Mem twin and the harness: no sleep, no real time, no network, no live store.
// The errCleared branch of shadowRead (a clear landing between two reads) is
// not covered: reaching it needs Mem.Fail at the pipelined load, which the
// harness's Sleep-free retry does not exercise in a way a test can hold.

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreTickCoverShadowPlansRunningAndWritesNothing: on a sprint with ready
// cards, its members up and the machine started (h.setup, startMachine),
// ShadowTick plans the tick on a read-only read and returns RUNNING, epoch 0,
// a plan whose Size is the sum of its parts' sizes and at least one part, and
// it writes nothing: the store's snapshot is byte for byte what it was.
func TestStoreTickCoverShadowPlansRunningAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	before, err := h.m.Snapshot()
	require.NoError(t, err)
	plan, err := h.st.ShadowTick(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "RUNNING", plan.State, "the state the shadow read")
	assert.EqualValues(t, 0, plan.Epoch, "the epoch the shadow read")
	assert.NotEmpty(t, plan.Parts, "the shadow planned the tick's moves")
	assert.Positive(t, plan.Size, "the shadow planned something to write")
	sum := 0
	for _, p := range plan.Parts {
		sum += p.Size
	}
	assert.Equal(t, sum, plan.Size, "the plan's size is the sum of its parts")
	assert.GreaterOrEqual(t, plan.Took, time.Duration(0), "the plan took a real, non-negative time")
	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after, "a shadow tick writes nothing")
}

// TestStoreTickCoverShadowStoppedWritesNothing: the same sprint with the
// machine never started: ShadowTick still plans (the state is reported) and
// says STOPPED, and it still writes nothing.
func TestStoreTickCoverShadowStoppedWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	before, err := h.m.Snapshot()
	require.NoError(t, err)
	plan, err := h.st.ShadowTick(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "STOPPED", plan.State, "the state the shadow read")
	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after, "a shadow tick writes nothing")
}

// TestStoreTickCoverShadowRefusesAnOwedClear: a clear that still owes its
// restore (an epoch advanced with no SettleEpoch) is refused before any read
// of the tables, with the restore named, and nothing is written: a shadow tick
// does not perform the restore a clear owes.
func TestStoreTickCoverShadowRefusesAnOwedClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	ok, err := h.m.AdvanceEpoch(h.ctx, 0, t0)
	require.NoError(t, err)
	require.True(t, ok, "the epoch advanced from 0")
	before, err := h.m.Snapshot()
	require.NoError(t, err)
	_, err = h.st.ShadowTick(h.ctx)
	require.Error(t, err)
	assert.ErrorContains(t, err, "still owes the restore")
	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after, "a shadow tick writes nothing")
}

// TestStoreTickCoverShadowRefusesABusyFence: an operation left pending in the
// fence is waited for, never repaired; past the store's attempts (set to 2,
// with the harness's Sleep a no-op) ShadowTick returns the busy refusal naming
// that it repairs nothing, and the pending operation is still in the fence
// afterwards.
func TestStoreTickCoverShadowRefusesABusyFence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	op := OpRecord{ID: "shadow-cut", Verb: "test", At: t0}
	h.pendingOp(op)
	h.st.Attempts = 2
	before, err := h.m.Snapshot()
	require.NoError(t, err)
	_, err = h.st.ShadowTick(h.ctx)
	require.Error(t, err)
	assert.ErrorContains(t, err, "the sprint is busy")
	assert.ErrorContains(t, err, "repairs nothing")
	pending := h.m.Pending()
	require.NotNil(t, pending, "the pending operation is still in the fence: no repair")
	assert.Equal(t, op.ID, pending.ID, "the operation the fence still holds")
	after, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, before, after, "a shadow tick writes nothing")
}

// TestStoreTickCoverShadowPlanSize: planSize is the whole of a plan's writes:
// its units, notes, rows, closes and updates. The empty plan is 0, each of the
// five kinds alone is one, and one of each sums to five.
func TestStoreTickCoverShadowPlanSize(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		plan sprint.Plan
		want int
	}{
		{name: "empty", plan: sprint.Plan{}, want: 0},
		{name: "one unit", plan: sprint.Plan{Units: []sprint.Unit{{}}}, want: 1},
		{name: "one note", plan: sprint.Plan{Notes: []sprint.Note{{}}}, want: 1},
		{name: "one row", plan: sprint.Plan{Rows: []sprint.RowAdd{{}}}, want: 1},
		{name: "one close", plan: sprint.Plan{Closes: []sprint.Open{{}}}, want: 1},
		{name: "one update", plan: sprint.Plan{Updates: []sprint.Note{{}}}, want: 1},
		{name: "one of each", plan: sprint.Plan{
			Units:   []sprint.Unit{{}},
			Notes:   []sprint.Note{{}},
			Rows:    []sprint.RowAdd{{}},
			Closes:  []sprint.Open{{}},
			Updates: []sprint.Note{{}},
		}, want: 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, planSize(tt.plan), tt.name)
		})
	}
}
