package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pause A immediately before it acknowledges its completed cleanup debt. B
// uses another wrapper over the same actual Mem, so the callback cannot enter
// A's hook recursively and no Mem mutex is held during the interleaving.
type beforeFleetDebtAck struct {
	*Mem
	once   sync.Once
	before func()
}

func (b *beforeFleetDebtAck) SetKey(ctx context.Context, name, value string) error {
	if name == keyDropDebt {
		var debt []string
		if err := json.Unmarshal([]byte(value), &debt); err == nil && len(debt) == 0 {
			b.once.Do(b.before)
		}
	}
	return b.Mem.SetKey(ctx, name, value)
}

// B's row removal succeeds but its first actual guarded beat deletion fails.
// The cleanup record must therefore retain B's discoverable retry work.
type failM3GuardedBeatDelete struct {
	*Mem
	failed bool
}

func (b *failM3GuardedBeatDelete) KeysDelIf(ctx context.Context, table string, guards []RowGuard) ([]string, error) {
	if !b.failed && slices.ContainsFunc(guards, func(g RowGuard) bool { return g.Row == "m3" }) {
		b.failed = true
		return nil, errors.New("injected m3 guarded beat cleanup failure")
	}
	return b.Mem.KeysDelIf(ctx, table, guards)
}

// A completed removal must acknowledge only A's debt. If a concurrent removal
// B recorded new debt then failed to delete its beat, A must not replace that
// new debt with its old snapshot. A retry with no remaining fleet row drift
// still has to discover and delete B's stale beat (FinishDrops is the exact
// production no-drift retry path called by fleet sync).
func TestFleetCleanupDebtAckPreservesConcurrentFailedRemoval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, m := range []string{"m1", "m2", "m3"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: 4}))
		zero := 0.0
		_, err := h.st.Beat(h.ctx, m, &zero, hostload.Source{})
		require.NoError(t, err)
	}
	// A's production sync removed m2 and kept m1/m3; its row cleanup follows.
	h.must(FleetStep(sprint.FleetReq{Op: "sync", Who: "tester", Sync: []sprint.SyncMember{{Name: "m1", Width: 4}, {Name: "m3", Width: 4}}, Machines: []string{"m1", "m3"}}))
	require.Nil(t, h.table().MemberCtl("m2"))
	backendB := &failM3GuardedBeatDelete{Mem: h.m}
	writerB := &Store{B: backendB, Names: h.st.Names, Actor: "tester", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	var fired bool
	backendA := &beforeFleetDebtAck{Mem: h.m, before: func() {
		fired = true
		// B reads the next inventory, removes m3, and records its cleanup debt
		// while A still has the earlier completed-debt acknowledgement pending.
		res, err := writerB.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "sync", Who: "tester", Sync: []sprint.SyncMember{{Name: "m1", Width: 4}}, Machines: []string{"m1"}}))
		require.NoError(t, err)
		require.Empty(t, res.Refused)
		deleted, err := writerB.DropMembers(h.ctx, []string{"m1"})
		require.ErrorContains(t, err, "injected m3 guarded beat cleanup failure")
		require.Equal(t, []string{"m3"}, deleted, "B committed its row removal before the key failure")
		require.True(t, backendB.failed)
		debt, err := h.st.dropDebt(h.ctx)
		require.NoError(t, err)
		require.Contains(t, debt, "m3", "B published discoverable retry work before A resumes")
		beats, err := h.st.Beats(h.ctx, []string{"m3"})
		require.NoError(t, err)
		require.Contains(t, beats, "m3", "the injected failure left B's stale beat")
	}}
	writerA := &Store{B: backendA, Names: h.st.Names, Actor: "tester", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	deleted, err := writerA.DropMembers(h.ctx, []string{"m1", "m3"})
	require.NoError(t, err)
	require.Equal(t, []string{"m2"}, deleted)
	require.True(t, fired, "the schedule reaches A's actual debt acknowledgement")
	snap := h.table()
	assert.False(t, snap.Fleet.HasRow("m2"))
	assert.False(t, snap.Fleet.HasRow("m3"))
	require.NotNil(t, snap.MemberCtl("m1"), "concurrent cleanup preserves the retained member")
	debt, err := h.st.dropDebt(h.ctx)
	require.NoError(t, err)
	assert.Contains(t, debt, "m3", "A must not erase B's newly published cleanup debt")

	require.Empty(t, sprint.FleetDrift(snap, []sprint.SyncMember{{Name: "m1", Width: 4}}, []string{"m1"}), "the retry has no row drift to rediscover B")
	require.NoError(t, h.st.FinishDrops(h.ctx), "run production fleet sync's no-drift cleanup retry")
	beats, err := h.st.Beats(h.ctx, []string{"m1", "m2", "m3"})
	require.NoError(t, err)
	assert.NotContains(t, beats, "m2")
	assert.NotContains(t, beats, "m3", "retry must delete the failed removal's stale beat")
	assert.Contains(t, beats, "m1", "cleanup must retain the remaining member's beat")
}
