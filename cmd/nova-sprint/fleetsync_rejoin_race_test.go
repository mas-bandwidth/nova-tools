package main

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Libraries considered: the existing syncApp/store.Mem rig, Backend embedding,
// and sync.Once provide an exact interleaving without processes or a clock wait.
// The hook survives Store's epoch pinning; app commands use the unwrapped Mem,
// so the rejoin cannot recursively enter the cleanup hook.
type rejoinBeforeRowsDel struct {
	store.Backend
	hook *rejoinRowsDelHook
}

type rejoinRowsDelHook struct {
	once   sync.Once
	before func()
}

func (b *rejoinBeforeRowsDel) AtEpoch(epoch uint64, old bool) store.Backend {
	return &rejoinBeforeRowsDel{Backend: b.Backend.AtEpoch(epoch, old), hook: b.hook}
}

func (b *rejoinBeforeRowsDel) RowsDel(ctx context.Context, table string, rows []string) error {
	if slices.Contains(rows, "m2") {
		b.hook.once.Do(b.hook.before)
	}
	return b.Backend.RowsDel(ctx, table, rows)
}

// RowsDelIf is the cleanup's delete since it is conditional at its commit: the
// same seam, between the cleanup's read and its delete.
func (b *rejoinBeforeRowsDel) RowsDelIf(ctx context.Context, table string, guards []store.RowGuard) ([]string, error) {
	if slices.ContainsFunc(guards, func(g store.RowGuard) bool { return g.Row == "m2" }) {
		b.hook.once.Do(b.hook.before)
	}
	return b.Backend.RowsDelIf(ctx, table, guards)
}

// A sync's post-step snapshot can precede another writer's fleet up and deal.
// SPEC-SPRINT section 5's inventory removal must not erase that new membership
// or its newly assigned work; section 6's primary invariant must still hold.
func TestFleetSyncCleanupPreservesARejoinAndFreshAssignment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("fleet down m1") // every fresh assignment must go to m2
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	want := []sprint.SyncMember{{Name: "m1", Width: 4}}
	machines := []string{"m1"}
	// Execute the production sync step, but leave its row cleanup to afterSync.
	res, err := st.Run(ctx, store.FleetStep(sprint.FleetReq{
		Op: "sync", Sync: want, Machines: machines, Who: "tester",
	}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	removed, err := st.Load(ctx, []string{sprint.Fleet, sprint.Work}, nil)
	require.NoError(t, err)
	require.True(t, removed.Fleet.HasRow("m2"))
	require.Nil(t, removed.MemberCtl("m2"), "the cleanup's candidate has no placed control card")

	var fired bool
	var workID, primaryID string
	hook := &rejoinRowsDelHook{before: func() {
		fired = true
		ta.ok("fleet up m2") // production RejoinMembers and fleet release
		ta.ok("add --stream fresh --count 1")
		ta.deal(1) // production DealStep assigns the fresh primary
		before, err := st.Load(ctx, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}, nil)
		require.NoError(t, err)
		require.NotNil(t, before.MemberCtl("m2"))
		require.Equal(t, sprint.Up, before.MemberCtl("m2").F("status"))
		cards := before.Fleet.Column(sprint.Ready, sprint.Working)
		require.Len(t, cards, 1, "one fresh assignment before stale cleanup")
		require.Equal(t, "m2", cards[0].Row)
		workID, primaryID = cards[0].ID, cards[0].F("primary")
		require.NotEmpty(t, primaryID)
		require.NotNil(t, before.Work.Placed(primaryID))
		require.Equal(t, sprint.Working, before.Work.Placed(primaryID).Col)
		require.Empty(t, sprint.Check(before, nil), "the interleaved state is valid before cleanup")
	}}
	cleanup := *st
	cleanup.B = &rejoinBeforeRowsDel{Backend: st.B, hook: hook}
	_, _, err = afterSync(ctx, &cleanup, want, machines)
	require.NoError(t, err)
	require.True(t, fired, "the interleaving must reach actual DropMembers.RowsDel")
	after, err := st.Load(ctx, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}, nil)
	require.NoError(t, err)
	assert.True(t, after.Fleet.HasRow("m2"), "stale cleanup must preserve the rejoined row")
	assert.NotNil(t, after.MemberCtl("m2"), "stale cleanup must preserve the rejoined control card")
	assert.NotNil(t, after.Fleet.Placed(workID), "stale cleanup must preserve newly assigned work")
	if assert.NotNil(t, after.Work.Placed(primaryID)) {
		assert.Equal(t, sprint.Working, after.Work.Placed(primaryID).Col)
		assert.Equal(t, workID, after.Work.Placed(primaryID).F("work"))
	}
	assert.Empty(t, sprint.Check(after, nil), "primary working must equal fleet ready plus working after cleanup")
}
