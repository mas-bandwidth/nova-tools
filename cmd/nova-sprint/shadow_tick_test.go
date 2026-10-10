package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShadowTickPlansOnTheStoreAndWritesNothing: tick --shadow on a sprint with a
// card ready plans the tick (a deal among its parts) and leaves the store byte for
// byte as it was; the tick by hand after it writes what the shadow planned
// (docs/SPEC-SPRINT.md section 14, install-canary-shadow-tick-r.w1).
func TestShadowTickPlansOnTheStoreAndWritesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, l := range []string{"init --readers reader-a --members m1", "add --stream s1 --count 1 --one", "start"} {
		code, _, errs := ta.do(l)
		require.Equal(t, 0, code, "%s: %s", l, errs)
	}
	before, err := ta.m.Snapshot()
	require.NoError(t, err)

	// run, not do: do beats every member first, a write of the harness's
	var out, errb bytes.Buffer
	code := ta.a.run([]string{"tick", "--shadow"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "SHADOW PLAN ")
	assert.Contains(t, out.String(), "SHADOW TICK OK epoch=0 state=RUNNING")
	assert.Contains(t, out.String(), "wrote=nothing")
	after, err := ta.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a shadow tick writes nothing")

	out.Reset()
	errb.Reset()
	code = ta.a.run([]string{"tick", "--shadow", "--json"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	plan, _, err := shadowLine(out.String())
	require.NoError(t, err)
	assert.Positive(t, plan.Size, "the shadow planned the tick's moves")
	assert.NotEmpty(t, plan.Parts)

	code, _, errs := ta.do("tick")
	require.Equal(t, 0, code, errs)
	ticked, err := ta.m.Snapshot()
	require.NoError(t, err)
	assert.NotEqual(t, string(after), string(ticked), "the tick by hand writes what the shadow planned")
}

// TestShadowTickStoreRefusesEveryWrite: the shadow tick's store user, a backend
// opened read-only, refuses each write the Backend and KV interfaces name and
// passes the reads; none of the optional writers is reachable through it.
func TestShadowTickStoreRefusesEveryWrite(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	code, _, errs := ta.do("init --readers reader-a --members m1")
	require.Equal(t, 0, code, errs)
	before, err := ta.m.Snapshot()
	require.NoError(t, err)
	ro := store.ReadOnly(ta.m)
	ctx := context.Background()
	assert.ErrorIs(t, ro.AtEpoch(1, false).SetCursor(ctx, "1-0"), store.ErrReadOnly, "a pinned read-only store stays read-only")
	kv := ro.(store.KV)
	writes := map[string]func() error{
		"Apply":          func() error { _, err := ro.Apply(ctx, ntable.BatchManifest{}); return err },
		"Create":         func() error { return ro.Create(ctx, ntable.Table{}) },
		"RowsAdd":        func() error { return ro.RowsAdd(ctx, "t", []string{"r"}) },
		"RowsHide":       func() error { return ro.RowsHide(ctx, "t", []string{"r"}) },
		"RowsShow":       func() error { return ro.RowsShow(ctx, "t", []string{"r"}) },
		"RowsDel":        func() error { return ro.RowsDel(ctx, "t", []string{"r"}) },
		"RowsDelIf":      func() error { _, err := ro.RowsDelIf(ctx, "t", nil); return err },
		"KeysDelIf":      func() error { _, err := ro.KeysDelIf(ctx, "t", nil); return err },
		"Place":          func() error { return ro.Place(ctx, "t", "r", "c", "id", 1) },
		"RowSet":         func() error { return ro.RowSet(ctx, "t", "r", nil) },
		"ViewSet":        func() error { return ro.ViewSet(ctx, ntable.View{}) },
		"ViewDelete":     func() error { return ro.ViewDelete(ctx, "v") },
		"DropTable":      func() error { return ro.DropTable(ctx, "t") },
		"CheckTable":     func() error { return ro.CheckTable(ctx, "t") },
		"AdvanceEpoch":   func() error { _, err := ro.AdvanceEpoch(ctx, 0, time.Time{}); return err },
		"SettleEpoch":    func() error { return ro.SettleEpoch(ctx, 0) },
		"Acquire":        func() error { _, err := ro.Acquire(ctx, 0, store.OpRecord{}); return err },
		"Release":        func() error { return ro.Release(ctx, store.OpRecord{}, true) },
		"SetReview":      func() error { return ro.SetReview(ctx, "n", time.Time{}, time.Time{}) },
		"SetCursor":      func() error { return ro.SetCursor(ctx, "1-0") },
		"SetCoordinator": func() error { return ro.SetCoordinator(ctx, "x") },
		"DeleteKeys":     func() error { _, err := ro.DeleteKeys(ctx, []string{"k"}); return err },
		"SetKey":         func() error { return kv.SetKey(ctx, "k", "v") },
		"SetKeyShowing":  func() error { return kv.SetKeyShowing(ctx, "k", "v", "view", "s") },
		"ShowState":      func() error { return kv.ShowState(ctx, "view", "s") },
	}
	for name, w := range writes {
		assert.True(t, errors.Is(w(), store.ErrReadOnly), "%s is refused", name)
	}
	_, isApplier := ro.(store.BatchApplier)
	_, isUnwrap := ro.(interface{ Unwrap() store.Backend })
	assert.False(t, isApplier, "no batch applier is reachable")
	assert.False(t, isUnwrap, "the backend beneath is not handed out")
	_, err = ro.Epoch(ctx)
	assert.NoError(t, err, "a read passes")
	after, err := ta.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}
