package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Mem embedding keeps the rig's real table, epoch, KV and membership
// behavior. Only one beat-key deletion fails, after real row deletion succeeds.
// This fixture stays at epoch 0, where Store retains the supplied backend.
type failRemovedBeatDeleteOnce struct {
	*store.Mem
	rowDeleted bool
	failed     bool
}

func (b *failRemovedBeatDeleteOnce) RowsDel(ctx context.Context, table string, rows []string) error {
	err := b.Mem.RowsDel(ctx, table, rows)
	if err == nil && slices.Contains(rows, "m2") {
		b.rowDeleted = true
	}
	return err
}

// RowsDelIf is the cleanup's row delete since it is conditional at its commit.
func (b *failRemovedBeatDeleteOnce) RowsDelIf(ctx context.Context, table string, guards []store.RowGuard) ([]string, error) {
	rows, err := b.Mem.RowsDelIf(ctx, table, guards)
	if err == nil && slices.Contains(rows, "m2") {
		b.rowDeleted = true
	}
	return rows, err
}

func (b *failRemovedBeatDeleteOnce) DeleteKeys(ctx context.Context, keys []string) (int, error) {
	if b.rowDeleted && !b.failed && slices.Contains(keys, (sprint.Names{}).Key("beat:m2")) {
		b.failed = true
		return 0, errors.New("injected removed-member beat deletion failure")
	}
	return b.Mem.DeleteKeys(ctx, keys)
}

// A row deletion that succeeds before beat cleanup fails must leave discoverable
// cleanup debt: rerunning the production fleet sync clears the stale beat even
// though the row is already gone. The removed member can still return normally.
func TestFleetSyncRetriesBeatCleanupAfterTheRowIsGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ta, inv := syncApp(t)
	ta.live = []string{"m1"} // the removed member must not recreate its beat between commands
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("fleet up m2") // one real beat, which the removal must delete
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	require.Zero(t, st.PinnedEpoch(), "the backend hook is retained at epoch 0")
	beats, err := st.Beats(ctx, []string{"m2"})
	require.NoError(t, err)
	require.Contains(t, beats, "m2", "the fixture must contain the removed member's beat")

	hook := &failRemovedBeatDeleteOnce{Mem: ta.m}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
		return hook, nil
	}
	inv.remove("m2")
	code, out, errs := ta.do("fleet sync")
	require.Equal(t, 2, code, "cleanup failure must be reported: %s%s", out, errs)
	require.Contains(t, errs, "injected removed-member beat deletion failure")
	require.True(t, hook.failed, "the fault must reach the actual beat deletion")
	require.True(t, hook.rowDeleted, "the actual row deletion succeeded before the fault")
	require.Nil(t, ta.fleetRows()["m2"], "the failed cleanup already removed the row")
	beats, err = st.Beats(ctx, []string{"m2"})
	require.NoError(t, err)
	require.Contains(t, beats, "m2", "the failed deletion leaves a stale beat to clean up")

	ta.ok("fleet sync") // retry the printed remedy through the production command
	beats, err = st.Beats(ctx, []string{"m2"})
	require.NoError(t, err)
	assert.NotContains(t, beats, "m2", "retry must complete beat cleanup even with no remaining fleet row")
	rows := ta.fleetRows()
	assert.Nil(t, rows["m2"], "cleanup retry must not rejoin an absent machine")
	require.NotNil(t, rows["m1"])
	assert.Equal(t, "4", rows["m1"]["width"], "cleanup must preserve the remaining fleet member")

	inv.set("m2", 6)
	ta.ok("fleet sync")
	ta.ok("fleet beat m2 --load 0") // the returning machine beats: a member is up while it beats
	ta.ok("fleet up m2")
	rows = ta.fleetRows()
	require.NotNil(t, rows["m2"], "a removed member still rejoins when its inventory row returns")
	assert.Equal(t, "6", rows["m2"]["width"])
	assert.Equal(t, sprint.Up, rows["m2"]["status"])
	beats, err = st.Beats(ctx, []string{"m2"})
	require.NoError(t, err)
	assert.Contains(t, beats, "m2", "fleet up keeps the returning member's fresh beat")
}
