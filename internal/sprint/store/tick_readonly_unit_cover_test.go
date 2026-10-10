package store

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreTickCoverReadOnlyDoubleWrap tests that ReadOnly given a readOnly returns it unchanged.
func TestStoreTickCoverReadOnlyDoubleWrap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ro := ReadOnly(h.m)
	roAgain := ReadOnly(ro)
	// Cast to readOnly and check underlying backend is the same
	roImpl := ro.(readOnly)
	roAgainImpl := roAgain.(readOnly)
	require.Equal(t, roImpl.b, roAgainImpl.b, "ReadOnly of a readOnly should return it unchanged")
}

// TestStoreTickCoverReadOnlyWrites tests that every write method returns ErrReadOnly.
func TestStoreTickCoverReadOnlyWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ro := ReadOnly(h.m)
	ctx := h.ctx

	// Capture snapshot before writes
	snapBefore, err := h.m.Snapshot()
	require.NoError(t, err)

	cases := []struct {
		name string
		do   func() error
	}{
		{"Apply", func() error { _, err := ro.Apply(ctx, ntable.BatchManifest{}); return err }},
		{"Create", func() error { return ro.Create(ctx, ntable.Table{}) }},
		{"RowsAdd", func() error { return ro.RowsAdd(ctx, "t", []string{"r"}) }},
		{"RowsHide", func() error { return ro.RowsHide(ctx, "t", []string{"r"}) }},
		{"RowsShow", func() error { return ro.RowsShow(ctx, "t", []string{"r"}) }},
		{"RowsDel", func() error { return ro.RowsDel(ctx, "t", []string{"r"}) }},
		{"RowsDelIf", func() error { _, err := ro.RowsDelIf(ctx, "t", []RowGuard{}); return err }},
		{"KeysDelIf", func() error { _, err := ro.KeysDelIf(ctx, "t", []RowGuard{}); return err }},
		{"Place", func() error { return ro.Place(ctx, "t", "r", "c", "v", 0) }},
		{"RowSet", func() error { return ro.RowSet(ctx, "t", "r", map[string]string{}) }},
		{"ViewSet", func() error { return ro.ViewSet(ctx, ntable.View{}) }},
		{"ViewDelete", func() error { return ro.ViewDelete(ctx, "t") }},
		{"DropTable", func() error { return ro.DropTable(ctx, "t") }},
		{"CheckTable", func() error { return ro.CheckTable(ctx, "t") }},
		{"AdvanceEpoch", func() error { _, err := ro.AdvanceEpoch(ctx, 1, h.now); return err }},
		{"SettleEpoch", func() error { return ro.SettleEpoch(ctx, 1) }},
		{"Acquire", func() error { _, err := ro.Acquire(ctx, 1, OpRecord{}); return err }},
		{"Release", func() error { return ro.Release(ctx, OpRecord{}, false) }},
		{"SetReview", func() error { return ro.SetReview(ctx, "n", h.now, h.now) }},
		{"SetCursor", func() error { return ro.SetCursor(ctx, "c") }},
		{"SetCoordinator", func() error { return ro.SetCoordinator(ctx, "c") }},
		{"DeleteKeys", func() error { _, err := ro.DeleteKeys(ctx, []string{"k"}); return err }},
	}

	// Test readOnly-specific writes (SetKey, SetKeyShowing, ShowState)
	roImpl := ro.(readOnly)
	roWriteCases := []struct {
		name string
		do   func() error
	}{
		{"SetKey", func() error { return roImpl.SetKey(ctx, "k", "v") }},
		{"SetKeyShowing", func() error { return roImpl.SetKeyShowing(ctx, "k", "v", "s", "h") }},
		{"ShowState", func() error { return roImpl.ShowState(ctx, "k", "v") }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.do()
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrReadOnly), "%s: error %v does not wrap ErrReadOnly", c.name, err)
		})
	}

	for _, c := range roWriteCases {
		t.Run(c.name, func(t *testing.T) {
			err := c.do()
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrReadOnly), "%s: error %v does not wrap ErrReadOnly", c.name, err)
		})
	}

	// Snapshot should be unchanged after writes
	snapAfter, err := h.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, snapBefore, snapAfter, "snapshot should be unchanged after write refusals")
}

// TestStoreTickCoverReadOnlyAtEpoch tests that AtEpoch returns a read-only backend.
func TestStoreTickCoverReadOnlyAtEpoch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ro := ReadOnly(h.m)
	ctx := h.ctx

	roAtEpoch := ro.AtEpoch(1, false).(readOnly)
	_, err := roAtEpoch.Apply(ctx, ntable.BatchManifest{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrReadOnly), "write on AtEpoch should return ErrReadOnly")
}

// TestStoreTickCoverReadOnlyReadPassThroughs tests that read pass-throughs return what Mem returns.
func TestStoreTickCoverReadOnlyReadPassThroughs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	ro := ReadOnly(h.m)
	ctx := h.ctx

	// Test Epoch
	epoch1, err := h.m.Epoch(ctx)
	require.NoError(t, err)
	epoch2, err := ro.Epoch(ctx)
	require.NoError(t, err)
	assert.Equal(t, epoch1, epoch2)

	// Test ReadFence
	fence1, err := h.m.ReadFence(ctx)
	require.NoError(t, err)
	fence2, err := ro.ReadFence(ctx)
	require.NoError(t, err)
	assert.Equal(t, fence1, fence2)

	// Test Coordinator
	coord1, err := h.m.Coordinator(ctx)
	require.NoError(t, err)
	coord2, err := ro.Coordinator(ctx)
	require.NoError(t, err)
	assert.Equal(t, coord1, coord2)

	// Test Cursor
	cur1, err := h.m.Cursor(ctx)
	require.NoError(t, err)
	cur2, err := ro.Cursor(ctx)
	require.NoError(t, err)
	assert.Equal(t, cur1, cur2)

	// Test Tails
	tails1, tails2, err := h.m.Tails(ctx)
	require.NoError(t, err)
	tails3, tails4, err := ro.Tails(ctx)
	require.NoError(t, err)
	assert.Equal(t, tails1, tails3)
	assert.Equal(t, tails2, tails4)

	// Test Shapes
	shapes1, err := h.m.Shapes(ctx, []string{"t-work"})
	require.NoError(t, err)
	shapes2, err := ro.Shapes(ctx, []string{"t-work"})
	require.NoError(t, err)
	assert.Equal(t, shapes1, shapes2)

	// Test CellIDs
	cellIDs1, err := h.m.CellIDs(ctx, shapes1)
	require.NoError(t, err)
	cellIDs2, err := ro.CellIDs(ctx, shapes2)
	require.NoError(t, err)
	assert.Equal(t, cellIDs1, cellIDs2)

	// Test ReadSet
	readSet1, err := h.m.ReadSet(ctx, "t-work", []string{"s1-1"})
	require.NoError(t, err)
	readSet2, err := ro.ReadSet(ctx, "t-work", []string{"s1-1"})
	require.NoError(t, err)
	assert.Equal(t, readSet1, readSet2)

	// Test OpenNotes
	notes1, err := h.m.OpenNotes(ctx)
	require.NoError(t, err)
	notes2, err := ro.OpenNotes(ctx)
	require.NoError(t, err)
	assert.Equal(t, notes1, notes2)

	// Test RecordIDs
	ids1, err := h.m.RecordIDs(ctx, "t-work")
	require.NoError(t, err)
	ids2, err := ro.RecordIDs(ctx, "t-work")
	require.NoError(t, err)
	assert.Equal(t, ids1, ids2)

	// Test QueueRead
	queue1, err := h.m.QueueRead(ctx)
	require.NoError(t, err)
	queue2, err := ro.QueueRead(ctx)
	require.NoError(t, err)
	assert.Equal(t, queue1, queue2)

	// Test Done
	done1, ok1, err := h.m.Done(ctx, "op")
	require.NoError(t, err)
	done2, ok2, err := ro.Done(ctx, "op")
	require.NoError(t, err)
	assert.Equal(t, done1, done2)
	assert.Equal(t, ok1, ok2)

	// Test DoneBefore
	doneBefore1, ok3, err := h.m.DoneBefore(ctx, "op", 1)
	require.NoError(t, err)
	doneBefore2, ok4, err := ro.DoneBefore(ctx, "op", 1)
	require.NoError(t, err)
	assert.Equal(t, doneBefore1, doneBefore2)
	assert.Equal(t, ok3, ok4)

	// Test Progress
	prog1, err := h.m.Progress(ctx)
	require.NoError(t, err)
	prog2, err := ro.Progress(ctx)
	require.NoError(t, err)
	assert.Equal(t, prog1, prog2)

	// Test Aliases
	aliases1, err := h.m.Aliases(ctx, []string{"a", "b"})
	require.NoError(t, err)
	aliases2, err := ro.Aliases(ctx, []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, aliases1, aliases2)

	// Test Answered
	answered1, err := h.m.Answered(ctx, []string{"id1", "id2"})
	require.NoError(t, err)
	answered2, err := ro.Answered(ctx, []string{"id1", "id2"})
	require.NoError(t, err)
	assert.Equal(t, answered1, answered2)

	// Test NotesSince
	notesSince1, ids1, err := h.m.NotesSince(ctx, "", 10)
	require.NoError(t, err)
	notesSince2, ids2, err := ro.NotesSince(ctx, "", 10)
	require.NoError(t, err)
	assert.Equal(t, notesSince1, notesSince2)
	assert.Equal(t, ids1, ids2)

	// Test LogSince
	logSince1, ids3, err := h.m.LogSince(ctx, "", 10)
	require.NoError(t, err)
	logSince2, ids4, err := ro.LogSince(ctx, "", 10)
	require.NoError(t, err)
	assert.Equal(t, logSince1, logSince2)
	assert.Equal(t, ids3, ids4)

	// Test GetKey and GetKeys through the read-only backend.
	require.NoError(t, h.m.SetKey(ctx, "test-key", "test-value"))
	kv1, ok1, err := h.m.GetKey(ctx, "test-key")
	require.NoError(t, err)
	kv2, ok2, err := ro.(KV).GetKey(ctx, "test-key")
	require.NoError(t, err)
	assert.Equal(t, kv1, kv2)
	assert.Equal(t, ok1, ok2)

	keys1, oks1, err := h.m.GetKeys(ctx, []string{"test-key", "missing"})
	require.NoError(t, err)
	keys2, oks2, err := ro.(KeysGetter).GetKeys(ctx, []string{"test-key", "missing"})
	require.NoError(t, err)
	assert.Equal(t, keys1, keys2)
	assert.Equal(t, oks1, oks2)
}

// TestStoreTickCoverReadOnlyKvless tests read methods over kvless backend.
func TestStoreTickCoverReadOnlyKvless(t *testing.T) {
	t.Parallel()
	// Create kvless backend
	kvlessBackend := kvless{NewMem()}
	ro := ReadOnly(kvlessBackend).(readOnly)
	ctx := context.Background()

	// Test GetKey returns "", false, nil
	kv, ok, err := ro.GetKey(ctx, "key")
	assert.Equal(t, "", kv)
	assert.False(t, ok)
	assert.NoError(t, err)

	// Test GetKeys returns slices with every ok false
	names := []string{"a", "b", "c"}
	keys, oks, err := ro.GetKeys(ctx, names)
	assert.NoError(t, err)
	assert.Len(t, keys, len(names))
	assert.Len(t, oks, len(names))
	for _, ok := range oks {
		assert.False(t, ok)
	}

	// Test Routes returns empty non-nil list with 0 round trips
	routes, trips, err := ro.Routes(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, routes.Routes)
	assert.Empty(t, routes.Routes)
	assert.Zero(t, trips)

	// Test PriceRoutes returns empty slice
	proutes, ptrips, err := ro.PriceRoutes(ctx)
	assert.NoError(t, err)
	assert.Empty(t, proutes)
	assert.Zero(t, ptrips)
}
