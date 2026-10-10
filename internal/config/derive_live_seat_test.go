package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFriendApplyRoleFollowsTheLiveSeatNotTheRow: a friend apply gives the
// coordinator role to the friend the live sprint:coordinator names, never to
// the stored sprint row's friend when the two disagree. The live seat is
// stella and the row names rowan: stella gains the role, rowan loses it.
func TestFriendApplyRoleFollowsTheLiveSeatNotTheRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	ap.views[KindSprint] = map[string]View{KindSprint: {"coordinator": "stella"}}

	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, err)

	require.Equal(t, "builder,coordinator,reader", ap.views[KindFriend]["stella"]["roles"],
		"stella's roles %q: the live seat holds the coordinator role", ap.views[KindFriend]["stella"]["roles"])
	require.Equal(t, "builder", ap.views[KindFriend]["rowan"]["roles"],
		"rowan's roles %q: the stored row's coordinator loses the role to the live seat", ap.views[KindFriend]["rowan"]["roles"])
}

// TestFriendApplyRoleUsesTheRowWhenRedisHoldsNoSprint: with no live seat
// (Redis holds no sprint row, a first apply) the role falls back to the
// stored sprint row's coordinator.
func TestFriendApplyRoleUsesTheRowWhenRedisHoldsNoSprint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()

	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, err)

	require.Equal(t, "builder,coordinator", ap.views[KindFriend]["rowan"]["roles"],
		"rowan's roles %q: the stored row's coordinator holds the role with no live seat", ap.views[KindFriend]["rowan"]["roles"])
	require.Equal(t, "builder,reader", ap.views[KindFriend]["stella"]["roles"],
		"stella's roles %q: no live seat names her", ap.views[KindFriend]["stella"]["roles"])
}

// TestFriendApplyRoleGainIsPlannedBeforeLoss: in res.Ops the op that gives
// the coordinator role is planned before the op that takes it, so
// ns_friend_roles never sees the loser written first (LASTCOORD).
func TestFriendApplyRoleGainIsPlannedBeforeLoss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, "builder,coordinator", ap.views[KindFriend]["rowan"]["roles"], "the first apply seats rowan")

	// The seat moves to stella in the live store; the row still names rowan.
	ap.views[KindSprint] = map[string]View{KindSprint: {"coordinator": "stella"}}

	var ops []Op
	_, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { ops = append(ops, op) })
	require.NoError(t, err)

	gain, loss := -1, -1
	for i, op := range ops {
		switch op.Op {
		case OpAdd, OpSet:
			has := hasWord(op.Row.Fields["roles"], CoordinatorRole)
			had := op.Prev != nil && hasWord(op.Prev["roles"], CoordinatorRole)
			switch {
			case has && !had:
				gain = i
			case had && !has:
				loss = i
			}
		}
	}
	require.NotEqual(t, -1, gain, "no op gives the coordinator role: %v", ops)
	require.NotEqual(t, -1, loss, "no op takes the coordinator role: %v", ops)
	require.Less(t, gain, loss, "the gain (op %d) is planned after the loss (op %d): %v", gain, loss, ops)
}
