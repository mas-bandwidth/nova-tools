package sprint_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The twin rigs' readers: two friends, ra and rb, whose roles name reader alone, with the
// friends' work off (set --friends off), so they are dealt every read card of a card in
// review and no work card (sprint read_cards.go). A rig makes them with friendReaders and
// beats them with beatFriendReaders.

// rigReaders is the stores whose rig made friend readers.
var rigReaders sync.Map

// friendReaders makes ra and rb the store's readers and beats them up.
func friendReaders(t *testing.T, st *store.Store, ctx context.Context) {
	t.Helper()
	_, _, _, err := st.SyncFriends(ctx, []store.FriendSpec{{Name: "ra", Width: 8, Class: "pro", Roles: sprint.RoleReader}, {Name: "rb", Width: 8, Class: "pro", Roles: sprint.RoleReader}})
	require.NoError(t, err)
	res, err := st.Run(ctx, store.SetStep(sprint.SetReq{Friends: sprint.SwitchOff, Who: "coordinator"}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	rigReaders.Store(st, true)
	beatFriendReaders(t, st, ctx)
}

// beatFriendReaders beats the store's friend readers, when its rig made them.
func beatFriendReaders(t *testing.T, st *store.Store, ctx context.Context) {
	t.Helper()
	if _, ok := rigReaders.Load(st); !ok {
		return
	}
	for _, f := range []string{"ra", "rb"} {
		_, err := st.FriendBeat(ctx, f)
		require.NoError(t, err)
		_, _, _, err = st.FriendHealth(ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: st.Now(), Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(t, err)
	}
}

// cutReads cuts the read cards every primary in review wants, as the tick's deal does
// first (sprint.CutReadCards), with the machine RUNNING or STOPPED.
func cutReads(t *testing.T, st *store.Store, ctx context.Context) {
	t.Helper()
	seats, err := st.FriendSeats(ctx, st.Now())
	require.NoError(t, err)
	due := 0
	step := store.TickPartStep("deal", func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
		return sprint.CutReadCards(s, seats), 0
	}, sprint.TickReq{Who: sprint.MachineActor, Friends: seats}, nil, nil, &due)
	step.Routes = true
	res, err := st.Run(ctx, step)
	require.NoError(t, err)
	require.Empty(t, res.Refused)
}

// placedReadCards is the primary's read cards placed on the fleet table.
func placedReadCards(s *sprint.Snapshot, id string) []*sprint.Card {
	var out []*sprint.Card
	for _, c := range s.Fleet.Column(sprint.Ready, sprint.Working) {
		if c.F("kind") == "read" && c.F("primary") == id {
			out = append(out, c)
		}
	}
	sprint.SortCards(out)
	return out
}
