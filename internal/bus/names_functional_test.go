//go:build functional

package bus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Redis.AddFriends is SADD on the set `friends`: it says which names were not there, a
// name added is a friend of the roster, and a send to her lands.
func TestRedisAddFriendsMakesARowAName(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	added, err := KnowFriends(ctx, b.Store, "rowan-space", "ada", "m1")
	require.NoError(t, err)
	assert.Equal(t, []string{"rowan-space"}, added)
	added, err = Redis{C: c}.AddFriends(ctx, "rowan-space", "cy")
	require.NoError(t, err)
	assert.Equal(t, []string{"cy"}, added, "a name there is not added again")
	friends, err := c.SMembers(ctx, friendsKey).Result()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ada", "bob", "rowan-space", "cy"}, friends)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan-space"}, Subject: "card dealt", Body: "x"})
	require.NoError(t, err)
	e, ok, err := b.Recv(ctx, "rowan-space", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "card dealt", e.Message().Subject)
}
