//go:build functional

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// On the real store the seat moves in the step's commit: the coordinator key,
// the seat's record, the log line and the note together (docs/SPEC-SPRINT.md,
// "Handing over the seat"); a take without the owner's name writes nothing.
func TestRedisTheSeatMovesInOneCommit(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	ctx := context.Background()
	require.NoError(t, st.B.SetCoordinator(ctx, "friend-a"))
	require.NoError(t, st.SetOwner(ctx, "owner-a"))

	st.Actor = "friend-b"
	res, err := st.Run(ctx, SeatStep(sprint.SeatReq{To: "friend-b", Who: "friend-b", Reason: "r", Take: true, Owner: "owner-a"}))
	require.NoError(t, err)
	require.NotEmpty(t, res.Refused, "a take with no owner's name was taken")
	holder, err := st.B.Coordinator(ctx)
	require.NoError(t, err)
	assert.Equal(t, "friend-a", holder)
	_, moved, err := st.Seat(ctx)
	require.NoError(t, err)
	assert.False(t, moved)

	res, err = st.Run(ctx, SeatStep(sprint.SeatReq{To: "friend-b", Who: "friend-b", Reason: "friend-a is asleep", Take: true, ApprovedBy: "owner-a", Owner: "owner-a"}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	holder, err = st.B.Coordinator(ctx)
	require.NoError(t, err)
	assert.Equal(t, "friend-b", holder)
	seat, moved, err := st.Seat(ctx)
	require.NoError(t, err)
	require.True(t, moved)
	assert.Equal(t, sprint.SeatChange{Holder: "friend-b", From: "friend-a", At: seat.At, By: "friend-b", Taken: true, ApprovedBy: "owner-a", Reason: "friend-a is asleep"}, seat)
	lines, err := st.Log(ctx)
	require.NoError(t, err)
	var said []string
	for _, l := range lines {
		said = append(said, sprint.Render(l))
	}
	assert.Contains(t, said, "seat TAKEN: friend-a -> friend-b, approved by owner-a: friend-a is asleep")
	v, err := st.Inbox(ctx, time.Hour, time.Hour, 100)
	require.NoError(t, err)
	found := false
	for _, g := range v.Groups {
		found = found || (g.Type == sprint.NSeatTaken && g.To == "friend-a")
	}
	assert.True(t, found, "the old holder's note: %+v", v.Groups)
}
