package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resources record on the twin (resource.go): who the tick counts as down,
// and a pass over a table with nothing held.

func resourceStore(t *testing.T) (*Store, *Mem, func(time.Duration)) {
	t.Helper()
	m := NewMem()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	n := 0
	st := &Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { return now },
		NewID: func() string { n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	ctx := context.Background()
	require.NoError(t, st.Init(ctx))
	require.NoError(t, m.RowsAdd(ctx, "t-readers", []string{"reader-a"}))
	require.NoError(t, m.SetCoordinator(ctx, "coordinator"))
	_, _, _, err := st.SyncFriends(ctx, []FriendSpec{{Name: "amy", Width: 1, Class: "pro"}})
	require.NoError(t, err)
	res, err := st.Run(ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	return st, m, func(d time.Duration) { now = now.Add(d) }
}

func TestResourceDownCountsFriendsMembersAndReadersNotUp(t *testing.T) {
	t.Parallel()
	st, _, _ := resourceStore(t)
	ctx := context.Background()
	down, err := st.ResourceDown(ctx, st.Now())
	require.NoError(t, err)
	assert.True(t, down("amy"), "a friend with no session evidence is down")
	assert.True(t, down("m1"), "a member that never beat is down")
	assert.True(t, down("reader-a"), "a reader that never beat is down")
	assert.False(t, down("zed"), "a name no table knows is not down: nothing can observe it")
	// amy's session answers a ping, m1 beats, reader-a asks for its queue: all up
	_, _, _, err = st.FriendHealth(ctx, "amy", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(t, err)
	zero := 0.0
	_, err = st.Beat(ctx, "m1", &zero, hostload.Source{})
	require.NoError(t, err)
	_, err = st.ReaderBeat(ctx, "reader-a")
	require.NoError(t, err)
	down, err = st.ResourceDown(ctx, st.Now())
	require.NoError(t, err)
	assert.False(t, down("amy"))
	assert.False(t, down("m1"))
	assert.False(t, down("reader-a"))
	// held is down for a lease: the coordinator's hold of m1
	res, err := st.Hold(ctx, sprint.HoldReq{Names: []string{"m1"}, Reason: "the cleaner", Who: "coordinator"})
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	down, err = st.ResourceDown(ctx, st.Now())
	require.NoError(t, err)
	assert.True(t, down("m1"), "a held member holds no resource")
}

func TestResourcesTickWithNothingHeldReadsNoTableAndWritesNothing(t *testing.T) {
	t.Parallel()
	st, m, _ := resourceStore(t)
	ctx := context.Background()
	res, changes, err := st.ResourcesTick(ctx)
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.Equal(t, 0, res.Notes)
	_, err = st.ResourceAdd(ctx, "bench-a", "bench", 1)
	require.NoError(t, err)
	before := m.Calls["apply"]
	_, changes, err = st.ResourcesTick(ctx)
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.Equal(t, before, m.Calls["apply"], "a table with no lease and no line is no step")
	rows, err := st.ResourceRows(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "bench-a", rows[0].Name)
}

func TestResourcesTickReleasesADownHoldersLeaseAndNotesIt(t *testing.T) {
	t.Parallel()
	st, _, advance := resourceStore(t)
	ctx := context.Background()
	_, err := st.ResourceAdd(ctx, "bench-a", "bench", 1)
	require.NoError(t, err)
	_, _, _, err = st.FriendHealth(ctx, "amy", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(t, err)
	ans, err := st.ResourceClaim(ctx, "bench-a", "amy", time.Hour)
	require.NoError(t, err)
	assert.True(t, ans.Granted)
	ans, err = st.ResourceClaim(ctx, "bench-a", "zed", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, ans.Place)
	// amy's session goes quiet past the pong window: she is down, and her lease goes
	advance(sprint.FriendPongWindow + time.Second)
	res, changes, err := st.ResourcesTick(ctx)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, []string{"amy"}, changes[0].Down)
	assert.Equal(t, []string{"zed"}, changes[0].Granted)
	assert.Equal(t, 1, res.Notes)
	rows, err := st.ResourceRows(ctx)
	require.NoError(t, err)
	require.Len(t, rows[0].Holders, 1)
	assert.Equal(t, "zed", rows[0].Holders[0].Who)
	assert.Equal(t, st.Now().Add(time.Hour), rows[0].Holders[0].Until)
}
