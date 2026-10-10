package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// countingBackend is a Mem wrapped to count the step's own commits (Acquire
// and Relock calls whose operation is not a lock) and RowsAdd and Place calls,
// so a test pins that a new-stream add makes one commit and no separate
// RowsAdd or Place: the step's rows and places ride the operation's commit,
// not passes of their own (this card's GOAL).
type countingBackend struct {
	*Mem
	mu       sync.Mutex
	acquires int
	rowsAdds int
	places   int
	// refuseAcquire, when above zero, makes the next that many non-lock
	// Acquire calls lose the generation race (false, nil) without touching the
	// store, as a commit that lost the race writes none of it.
	refuseAcquire int
	// afterRefuse runs right after a refused Acquire, while the store is
	// still unwritten by it; a test reads the stream row here.
	afterRefuse func()
}

func (c *countingBackend) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	c.mu.Lock()
	if !op.Lock {
		c.acquires++
	}
	if c.refuseAcquire > 0 && !op.Lock {
		c.refuseAcquire--
		c.mu.Unlock()
		if c.afterRefuse != nil {
			c.afterRefuse()
		}
		return false, nil
	}
	c.mu.Unlock()
	return c.Mem.Acquire(ctx, gen, op)
}

func (c *countingBackend) Relock(ctx context.Context, held string, op OpRecord) (bool, error) {
	c.mu.Lock()
	c.acquires++
	c.mu.Unlock()
	return c.Mem.Relock(ctx, held, op)
}

func (c *countingBackend) RowsAdd(ctx context.Context, table string, rows []string) error {
	c.mu.Lock()
	c.rowsAdds++
	c.mu.Unlock()
	return c.Mem.RowsAdd(ctx, table, rows)
}

func (c *countingBackend) Place(ctx context.Context, table, row, col, id string, score float64) error {
	c.mu.Lock()
	c.places++
	c.mu.Unlock()
	return c.Mem.Place(ctx, table, row, col, id, score)
}

func (c *countingBackend) counts() (acquires, rowsAdds, places int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acquires, c.rowsAdds, c.places
}

// wrapCounting hands the harness's store a counting backend over the same Mem,
// so the setup (init, readers, beats) is behind it and only the step under
// test is counted.
func wrapCounting(h *harness, cb *countingBackend) {
	h.t.Helper()
	cb.Mem = h.m
	h.st.B = cb
}

// TestANewStreamAndItsFirstCardCommitInOneWrite pins the fix for the
// 2026-10-07 invisible card: a card added to a new stream is written by one
// Acquire carrying the stream's rows, not by a separate RowsAdd pass that a
// reader between the writes (or a failure after the first) saw as a stream
// with no card. After add returns, the stream row and the card are both there.
func TestANewStreamAndItsFirstCardCommitInOneWrite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cb := &countingBackend{}
	wrapCounting(h, cb)

	res := h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
	assert.Len(t, res.Moved, 1, "the card moved once")

	acquires, rowsAdds, places := cb.counts()
	assert.Equal(t, 1, acquires, "one Acquire carries the stream's rows and the card")
	assert.Zero(t, rowsAdds, "no separate RowsAdd pass")
	assert.Zero(t, places, "no separate Place pass")

	snap := h.snap()
	assert.True(t, snap.Work.HasRow("s2"), "the stream row is on the work table")
	assert.True(t, snap.Merge.HasRow("s2"), "the stream row is on the merge table")
	assert.Equal(t, 1, snap.Work.Count("s2", sprint.Ready), "the card is read back")
}

// TestALostCommitLeavesNoStreamRow pins the other half of the guarantee: a
// commit that loses the generation race writes none of it, so the stream row
// does not appear before the operation that writes the card. The retried
// commit lands, and then both the stream row and the card are there.
func TestALostCommitLeavesNoStreamRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cb := &countingBackend{refuseAcquire: 1}
	wrapCounting(h, cb)

	sawRow := false
	cb.afterRefuse = func() {
		s := h.snap()
		sawRow = sawRow || s.Work.HasRow("s2") || s.Merge.HasRow("s2")
	}

	res := h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
	assert.Len(t, res.Moved, 1, "the card moved once")
	assert.False(t, sawRow, "the lost commit wrote a stream row with no card")

	acquires, _, _ := cb.counts()
	assert.Equal(t, 2, acquires, "the lost try and the retried commit")

	snap := h.snap()
	assert.True(t, snap.Work.HasRow("s2"), "the stream row is on the work table")
	assert.True(t, snap.Merge.HasRow("s2"), "the stream row is on the merge table")
	assert.Equal(t, 1, snap.Work.Count("s2", sprint.Ready), "the card is read back")
	require.NotNil(t, snap.Work.Card("s2-1"), "the card is on the table")
}

// TestARefusedNewStreamAddWritesNoStreamRow pins the other refusal: a write
// the store will not take is validated whole before any of it mutates Mem, so
// a rejected add of a card to a new stream leaves neither stream row, not just
// no card. Acquire added the rows before it marshalled and checked MaxWrite,
// recreating the stream-without-card state this card makes all-or-nothing.
func TestARefusedNewStreamAddWritesNoStreamRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.m.MaxWrite = 512

	res, err := h.st.Run(h.ctx, AddStep(sprint.AddReq{Stream: "s2", Count: 20}))
	require.False(t, err == nil && len(res.Refused) == 0, "a record over the store's bound was taken: %+v", res)
	require.Contains(t, fmt.Sprint(err, res.Refused), "broken pipe", "the refusal does not name the store's reason: %v %+v", err, res.Refused)

	snap := h.snap()
	assert.False(t, snap.Work.HasRow("s2"), "a rejected new-stream add left the work stream row")
	assert.False(t, snap.Merge.HasRow("s2"), "a rejected new-stream add left the merge stream row")
	assert.Equal(t, 0, snap.Work.Count("s2", sprint.Ready), "a rejected new-stream add left the card")
	h.clean("refused")
}
