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

// This seam runs after cleanup has read an off-table control card, immediately
// before its actual beat deletion. The interleaved commands use the plain app
// backend, so they cannot recursively enter this seam.
type rejoinBeforeBeatDelete struct {
	store.Backend
	store.KV // keep cleanup debt and beat records on the actual root backend
	hook     *rejoinBeatDeleteHook
}

var _ store.KV = (*rejoinBeforeBeatDelete)(nil)

type rejoinBeatDeleteHook struct {
	once   sync.Once
	before func()
}

func (b *rejoinBeforeBeatDelete) AtEpoch(epoch uint64, old bool) store.Backend {
	return &rejoinBeforeBeatDelete{Backend: b.Backend.AtEpoch(epoch, old), KV: b.KV, hook: b.hook}
}

func (b *rejoinBeforeBeatDelete) KeysDelIf(ctx context.Context, table string, guards []store.RowGuard) ([]string, error) {
	if slices.ContainsFunc(guards, func(g store.RowGuard) bool { return g.Row == "m2" }) {
		b.hook.once.Do(b.hook.before)
	}
	return b.Backend.KeysDelIf(ctx, table, guards)
}

func (b *rejoinBeforeBeatDelete) DeleteKeys(ctx context.Context, keys []string) (int, error) {
	if slices.Contains(keys, (sprint.Names{}).Key("beat:m2")) {
		b.hook.once.Do(b.hook.before)
	}
	return b.Backend.DeleteKeys(ctx, keys)
}

// A completed fresh beat and fleet up must stay fresh when stale cleanup loses
// its row guard. SPEC-SPRINT section 5 takes work back only after missed beat
// windows; this schedule advances no clock and sends no later beat. Losing the
// beat can leave the primary invariant valid while incorrectly withdrawing
// newly assigned work on the very next tick.
func TestFleetSyncStaleCleanupPreservesARejoinedMembersFreshBeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ta, inv := syncApp(t)
	ta.live = []string{"m1"} // no fixture-generated m2 beat can hide the deletion
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("fleet down m1")
	// Independent readers/tick do not share the app's retained read twin.
	authoritative := func() *store.Store {
		return &store.Store{B: ta.m, Names: sprint.Names{}, Actor: "tester", Now: ta.a.now, NewID: store.NewID, ByHand: true}
	}
	st := authoritative()
	want := []sprint.SyncMember{{Name: "m1", Width: 4}}
	machines := []string{"m1"}
	res, err := st.Run(ctx, store.FleetStep(sprint.FleetReq{Op: "sync", Sync: want, Machines: machines, Who: "tester"}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	removed, err := st.Load(ctx, []string{sprint.Fleet, sprint.Work}, nil)
	require.NoError(t, err)
	require.True(t, removed.Fleet.HasRow("m2"))
	require.Nil(t, removed.MemberCtl("m2"))

	var fired bool
	var workID, primaryID string
	var fresh sprint.Beat
	hook := &rejoinBeatDeleteHook{before: func() {
		fired = true
		ta.ok("fleet beat m2 --load 0")
		ta.ok("fleet up m2")
		ta.ok("add --stream fresh --count 1")
		ta.deal(1)
		before, err := authoritative().Load(ctx, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}, nil)
		require.NoError(t, err)
		require.NotNil(t, before.MemberCtl("m2"))
		require.Equal(t, sprint.Up, before.MemberCtl("m2").F("status"))
		cards := before.Fleet.Column(sprint.Ready, sprint.Working)
		require.Len(t, cards, 1)
		require.Equal(t, "m2", cards[0].Row)
		workID, primaryID = cards[0].ID, cards[0].F("primary")
		require.NotEmpty(t, primaryID)
		require.NotNil(t, before.Work.Placed(primaryID), "the primary is committed before the cleanup seam resumes")
		require.Empty(t, sprint.Check(before, nil))
		beats, err := authoritative().Beats(ctx, []string{"m2"})
		require.NoError(t, err)
		require.Contains(t, beats, "m2")
		fresh = beats["m2"]
		require.True(t, fresh.Alive(ta.a.now()), "the rejoin completed with a fresh real beat")
	}}
	cleanup := *st
	kv, ok := st.B.(store.KV)
	require.True(t, ok, "the real backend must keep the cleanup debt and beat records")
	cleanup.B = &rejoinBeforeBeatDelete{Backend: st.B, KV: kv, hook: hook}
	_, _, err = afterSync(ctx, &cleanup, want, machines)
	require.NoError(t, err)
	require.True(t, fired, "the schedule must enter the actual beat cleanup")
	after, err := authoritative().Load(ctx, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}, nil)
	require.NoError(t, err)
	assert.True(t, after.Fleet.HasRow("m2"))
	assert.NotNil(t, after.MemberCtl("m2"))
	assert.NotNil(t, after.Fleet.Placed(workID), "the row guard preserves the fresh assignment")
	assert.Empty(t, sprint.Check(after, nil), "this is a presence failure, not a broken primary invariant")
	beats, err := authoritative().Beats(ctx, []string{"m2"})
	require.NoError(t, err)
	if assert.Contains(t, beats, "m2", "stale cleanup must preserve the completed returning member's beat") {
		assert.Equal(t, fresh, beats["m2"])
	}
	assert.True(t, beats["m2"].Alive(ta.a.now()), "no time elapsed or beat window was missed")

	ta.ok("start")                     // start only after the add/deal's stopped-sprint precondition is committed
	_, err = authoritative().Tick(ctx) // real tick, without testApp's automatic beats
	require.NoError(t, err)
	afterTick, err := authoritative().Load(ctx, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}, nil)
	require.NoError(t, err)
	assert.NotNil(t, afterTick.Fleet.Placed(workID), "the next tick must keep the freshly assigned work on m2")
	if assert.NotNil(t, afterTick.Work.Placed(primaryID)) {
		assert.Equal(t, sprint.Working, afterTick.Work.Placed(primaryID).Col)
	}
	assert.Empty(t, sprint.Check(afterTick, nil), "a withdrawal can preserve Rule 2 and still violate fresh presence")
}
