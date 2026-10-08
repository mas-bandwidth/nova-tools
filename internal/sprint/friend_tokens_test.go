package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendTokensSumPerFriendFromTheResultUsage is the friends table's tokens
// column from its source: each friend card's usage, as the result the friend's
// session writes carries it (`usage: in= out= cache=`), read from her report and
// summed over her cards' `usage` fields. The unit is no store and no clock; the
// sum over a friend's fleet row is proved where the fleet table has cards
// (cmd/nova-sprint, TestTheFriendsTableCountsTheFriendsSprintCards).
func TestFriendTokensSumPerFriendFromTheResultUsage(t *testing.T) {
	t.Parallel()

	// the result's own compact line: in, out and cache are cardcost's input,
	// output and cache read
	u, ok := FriendUsage("# card\n\n**Verdict:** LAND\nusage: in=1000 out=2000 cache=300\n")
	require.True(t, ok)
	assert.Equal(t, int64(3300), friendUsageTokens(u), "in + out + cache, the classes the column counts")
	assert.Equal(t, "3.3K", CompactTokens(friendUsageTokens(u)))

	// the machinery's own Cost: line carries the same counts under cardcost's names
	u, ok = FriendUsage("Cost: $0.25 tokens input=1000 cache_read=300 output=2000 reasoning=7\n")
	require.True(t, ok)
	assert.Equal(t, int64(3300), friendUsageTokens(u), "reasoning is not one of the four classes")

	// a card with no usage in its report adds nothing
	_, ok = FriendUsage("Verdict: HOLD\nno usage here\n")
	assert.False(t, ok)

	// a count that is negative, a word or a fraction is skipped, and absent
	u, ok = FriendUsage("usage: in=-1 out=nope cache=1.5\n")
	require.True(t, ok)
	assert.Equal(t, int64(0), friendUsageTokens(u), "no count read is no tokens")

	// two of one friend's cards sum, each card's usage field read once
	cards := []*Card{
		{Fields: map[string]string{FieldUsage: "input=1000 output=2000 cache_read=300"}},
		{Fields: map[string]string{FieldUsage: "input=400 output=500 cache_read=400 cache_write=100"}},
	}
	assert.Equal(t, int64(4700), FriendTokensFromCards(cards), "1000+2000+300 and 400+500+400+100")
	assert.Equal(t, "4.7K", CompactTokens(FriendTokensFromCards(cards)))

	// the cell: a subscription friend reads the compact count, an api-billed friend
	// the dollars her records charged rounded up to the cent
	assert.Equal(t, "1.2M", FriendTokensCell(1_200_000, "", ""), "no billing is a subscription")
	assert.Equal(t, "1.2M", FriendTokensCell(1_200_000, "9.99", "subscription"))
	assert.Equal(t, "$1.24", FriendTokensCell(1_200_000, "1.231", "api"))
	assert.Equal(t, "$0.01", FriendTokensCell(0, "0.001", "metered"))
	assert.Equal(t, "0", FriendTokensCell(0, "", ""))
	assert.Equal(t, "1.3M", CompactTokens(1_250_000), "one decimal, half up")
}
