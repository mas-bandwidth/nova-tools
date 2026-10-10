package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// clearOnCall clears the sprint (through another store) at the n-th call of
// a kind of the wrapped store: fence (ReadFence), shapes, acquire, apply or
// release. It keeps the machine's records of the store it wraps.
type clearOnCall struct {
	Backend
	kv    KV
	kind  string
	n     int
	seen  int
	clear func()
}

func (c *clearOnCall) at(kind string) {
	if kind != c.kind {
		return
	}
	c.seen++
	if c.seen == c.n {
		c.clear()
	}
}

func (c *clearOnCall) GetKey(ctx context.Context, name string) (string, bool, error) {
	return c.kv.GetKey(ctx, name)
}

func (c *clearOnCall) SetKey(ctx context.Context, name, value string) error {
	return c.kv.SetKey(ctx, name, value)
}

func (c *clearOnCall) SetKeyShowing(ctx context.Context, name, value, view, state string) error {
	return c.kv.SetKeyShowing(ctx, name, value, view, state)
}

func (c *clearOnCall) ShowState(ctx context.Context, view, state string) error {
	return c.kv.ShowState(ctx, view, state)
}

func (c *clearOnCall) ReadFence(ctx context.Context) (Fence, error) {
	c.at("fence")
	return c.Backend.ReadFence(ctx)
}

func (c *clearOnCall) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	c.at("shapes")
	return c.Backend.Shapes(ctx, tables)
}

func (c *clearOnCall) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	c.at("acquire")
	return c.Backend.Acquire(ctx, gen, op)
}

func (c *clearOnCall) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	c.at("apply")
	return c.Backend.Apply(ctx, m)
}

func (c *clearOnCall) Release(ctx context.Context, op OpRecord, commit bool) error {
	c.at("release")
	return c.Backend.Release(ctx, op, commit)
}

// AtEpoch keeps the wrapper on the pinned backend.
func (c *clearOnCall) AtEpoch(epoch uint64, old bool) Backend {
	if old {
		return c.Backend.AtEpoch(epoch, old)
	}
	w := *c
	w.Backend = c.Backend.AtEpoch(epoch, old)
	return &w
}

// withoutMachine is an image with the machine's records left out: they are
// the sprint's, of no epoch.
func withoutMachine(img string) string {
	var out []string
	for _, l := range strings.Split(img, "\n") {
		if !strings.Contains(l, "sprint:heartbeat") && !strings.Contains(l, "sprint:machine") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// C6: a writer whose step the clear finished at the old epoch, as the step
// ran, is told the sprint was cleared, not what the sync of the display cells
// met.
func TestAWriterCaughtMidStepIsToldTheSprintWasCleared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	st := *h.st
	st.B = &clearOnCall{Backend: h.m, kv: h.m, kind: "release", n: 1, clear: func() {
		_, err := h.st.Clear(h.ctx)
		assert.NoError(t, err, "clear: %v", err)
	}}
	_, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	var ce *ClearedError
	require.ErrorAs(t, err, &ce, "the writer was told: %v", err)
	require.True(t, ce.Finished, "the writer was told: %v", err)
	require.Contains(t, err.Error(), "the sprint was cleared at", "the writer was told: %v", err)
	require.Contains(t, err.Error(), "stand at epoch 0", "the writer was told: %v", err)
	s, _ := h.st.At(0).Load(h.ctx, All, nil)
	require.NotNil(t, s, "the step did not finish at epoch 0")
	require.Equal(t, sprint.Working, s.StateOf("s1-1"), "the step did not finish at epoch 0")
	h.clean("after")
}

// C6: a step holding an epoch the sprint has not reached is not told the
// sprint was cleared.
func TestAStepAheadOfTheSprintIsNotToldCleared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	ahead := uint64(5)
	step := FleetStep(sprint.FleetReq{Op: "down", Member: "m1"})
	step.Epoch = &ahead
	res, err := h.st.Run(h.ctx, step)
	require.NoError(t, err, "a step ahead: %+v %v", res, err)
	require.Len(t, res.Refused, 1, "a step ahead: %+v %v", res, err)
	require.NotContains(t, res.Refused[0].Why, "cleared at", "a step ahead: %+v %v", res, err)
	require.Contains(t, res.Refused[0].Why, "unknown to this sprint", "a step ahead: %+v %v", res, err)
}
