//go:build functional

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A clear after a cleanup's guard read freezes its old control record. A member
// added in the new epoch has a new record, but its beat remains deployment-wide
// (docs/SPEC-SPRINT.md section 5; store.Clear, FinishDrops). The old conditional
// cleanup must not delete that new member's fresh beat.
func TestRedisFleetBeatGuardPreservesFreshBeatAfterClearBeforeExec(t *testing.T) {
	t.Parallel()
	st, reader := liveStore(t)
	ctx := context.Background()
	h := &harness{t: t, st: st, ctx: ctx, now: time.Now()}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(FleetStep(sprint.FleetReq{Op: "sync", Who: "functional", Sync: []sprint.SyncMember{{Name: "m1", Width: 4}}, Machines: []string{"m1"}}))
	pinned, err := st.Pinned(ctx)
	require.NoError(t, err)
	extras := sprint.NamedExtras(sprint.Fleet, []string{sprint.CtlID("m2")})
	snap, err := pinned.Load(ctx, []string{sprint.Fleet, sprint.Work}, extras)
	require.NoError(t, err)
	require.Nil(t, snap.MemberCtl("m2"), "the old control card must be off the table")
	require.True(t, snap.Fleet.HasRow("m2"))
	rec := snap.Fleet.Card(sprint.CtlID("m2"))
	require.NotNil(t, rec)
	require.False(t, rec.Placed())
	id := pinned.sid(sprint.CtlID("m2"))
	key := pinned.Names.Key(beatKey("m2"))
	guard := RowGuard{Row: "m2", ID: id, Key: pinned.Names.RecordKey(sprint.Fleet, id), Rev: rec.Rev, Keys: []string{key}}
	table := pinned.Names.Table(sprint.Fleet)
	deleted, err := pinned.B.RowsDelIf(ctx, table, []RowGuard{guard})
	require.NoError(t, err)
	require.Equal(t, []string{"m2"}, deleted, "prepare only the completed row deletion")
	snap, err = pinned.Load(ctx, []string{sprint.Fleet, sprint.Work}, extras)
	require.NoError(t, err)
	require.False(t, snap.Fleet.HasRow("m2"), "the beat guard requires an absent old row")
	rec = snap.Fleet.Card(sprint.CtlID("m2"))
	require.NotNil(t, rec)
	require.False(t, rec.Placed())
	require.Equal(t, guard.Rev, rec.Rev, "the old off-table record still satisfies the guard")
	oldLoad := 13.0
	_, err = st.Beat(ctx, "m2", &oldLoad, hostload.Source{NCPU: 8})
	require.NoError(t, err)
	oldBeat, err := reader.Get(ctx, key).Result()
	require.NoError(t, err)

	options := *reader.Options()
	writer := redis.NewClient(&options)
	t.Cleanup(func() { _ = writer.Close() })
	other := &Store{B: &Redis{C: writer, Names: st.Names, Now: time.Now}, Names: st.Names, Actor: "functional", NewID: NewID}
	r := pinned.B.(*Redis)
	var once sync.Once
	var fired bool
	var freshBeat string
	r.beforeExec = func() {
		once.Do(func() {
			fired = true
			cleared, err := other.Clear(ctx)
			require.NoError(t, err)
			require.Equal(t, pinned.PinnedEpoch(), cleared.From)
			require.Equal(t, pinned.PinnedEpoch()+1, cleared.To)
			res, err := other.Run(ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Who: "functional"}))
			require.NoError(t, err)
			require.Empty(t, res.Refused)
			current, err := other.Pinned(ctx)
			require.NoError(t, err)
			require.NotEqual(t, guard.ID, current.sid(sprint.CtlID("m2")), "the new member has a different stored control id")
			snap, err := current.Load(ctx, []string{sprint.Fleet}, nil)
			require.NoError(t, err)
			require.True(t, snap.Fleet.HasRow("m2"))
			require.NotNil(t, snap.MemberCtl("m2"), "the new-epoch member is placed before the old EXEC")
			freshLoad := 47.0
			_, err = other.Beat(ctx, "m2", &freshLoad, hostload.Source{NCPU: 8})
			require.NoError(t, err)
			freshBeat, err = writer.Get(ctx, key).Result()
			require.NoError(t, err)
			require.NotEqual(t, oldBeat, freshBeat, "prove the beat was replaced after the clear")
		})
	}
	t.Cleanup(func() { r.beforeExec = nil })
	deleted, err = pinned.B.KeysDelIf(ctx, table, []RowGuard{guard})
	require.True(t, fired, "clear must occur after a passing guard read, before its EXEC")
	if err != nil {
		var cleared *ClearedError
		if !errors.As(err, &cleared) {
			assert.Contains(t, []string{"STALE", "MEMBEREPOCH"}, refusalCode(err), "only a legitimate stale-epoch refusal is accepted: %v", err)
		}
	}
	assert.Empty(t, deleted, "the stale guard must not claim the new member's beat was deleted")
	got, err := writer.Get(ctx, key).Result()
	assert.NoError(t, err, "the fresh root beat must still exist")
	assert.Equal(t, freshBeat, got, "the fresh new-epoch beat must survive the stale EXEC")
}
