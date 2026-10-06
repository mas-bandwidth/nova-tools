package sprint_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokensT0 is the injected clock. The test opens no socket and calls no time.Now.
var tokensT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// TestFriendTokensSumPerFriendFromTheResultUsage is the friends table's token
// count: each friend card's RESULT.md line usage: in= out= cache= is summed
// per friend, a re-note of the same card replaces, and a body with no usage
// line adds nothing. Empty billing is subscription and shows the compact count.
func TestFriendTokensSumPerFriendFromTheResultUsage(t *testing.T) {
	t.Parallel()
	m := store.NewMem()
	st := &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now: func() time.Time { return tokensT0 }, NewID: func() string { return "1" }, Sleep: func(time.Duration) {}}
	ctx := context.Background()
	require.NoError(t, st.Init(ctx))
	_, _, _, err := st.SyncFriends(ctx, []store.FriendSpec{
		{Name: "amy", Width: 2}, {Name: "bob", Width: 1}, {Name: "cat", Width: 1},
	})
	require.NoError(t, err)

	require.NoError(t, st.NoteFriendResult(ctx, "amy", "card-1", "usage: in=1000000 out=150000 cache=50000\n"))
	require.NoError(t, st.NoteFriendResult(ctx, "amy", "card-2", "done\nusage: out=0 cache=0 in=200000 usd=50\n"))
	// the same card noted again does not double
	require.NoError(t, st.NoteFriendResult(ctx, "amy", "card-1", "usage: in=1000000 out=150000 cache=50000\n"))
	// a body with no usage line adds nothing and leaves the earlier notes
	require.NoError(t, st.NoteFriendResult(ctx, "amy", "card-3", "Verdict: LAND\nno tokens here\n"))
	// a later body of the same card with no usage line leaves the earlier note
	require.NoError(t, st.NoteFriendResult(ctx, "amy", "card-1", "no usage this time\n"))
	require.NoError(t, st.NoteFriendResult(ctx, "bob", "card-9", "usage: cache=5 in=3 out=4\n"))

	cell := func(name string) (sprint.ResultUsage, string) {
		t.Helper()
		rows, err := st.FriendRows(ctx, tokensT0)
		require.NoError(t, err)
		for _, r := range rows {
			if r.Name == name {
				return r.Usage, sprint.FriendTokensCell(r.Usage, r.Billing)
			}
		}
		t.Fatalf("no friend %s", name)
		return sprint.ResultUsage{}, ""
	}
	amy, amyCell := cell("amy")
	assert.Equal(t, sprint.ResultUsage{In: 1_200_000, Out: 150_000, Cache: 50_000, USD: "50"}, amy)
	assert.Equal(t, "1.4M", amyCell, "1_200_000 + 200_000, and usd is ignored while billing is empty")
	bob, bobCell := cell("bob")
	assert.Equal(t, sprint.ResultUsage{In: 3, Out: 4, Cache: 5}, bob)
	assert.Equal(t, "12", bobCell)
	_, catCell := cell("cat")
	assert.Equal(t, "0", catCell, "a friend with no note shows zero")

	// friend sync keeps the sums when it rewrites width
	_, _, updated, err := st.SyncFriends(ctx, []store.FriendSpec{
		{Name: "amy", Width: 4}, {Name: "bob", Width: 1}, {Name: "cat", Width: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"amy"}, updated)
	_, amyCell = cell("amy")
	assert.Equal(t, "1.4M", amyCell, "a sync that changes width leaves the sum")

	// api billing shows the summed usd, rounded up to the cent; the token sum stays
	require.NoError(t, st.SetFriendBilling(ctx, "bob", "api"))
	require.NoError(t, st.NoteFriendResult(ctx, "bob", "card-9", "usage: in=3 out=4 cache=5 usd=1.231\n"))
	require.NoError(t, st.NoteFriendResult(ctx, "bob", "card-8", "usage: in=1 usd=0.001\n"))
	bob, bobCell = cell("bob")
	assert.Equal(t, int64(4), bob.In)
	assert.Equal(t, "$1.24", bobCell, "1.231 + 0.001 rounds up to the next cent")

	require.Error(t, st.NoteFriendResult(ctx, "amy", "", "usage: in=1\n"), "an empty card id is refused")
	require.Error(t, st.NoteFriendResult(ctx, "nope", "card-1", "usage: in=1\n"), "a friend the roster lacks is refused")
	_, amyCell = cell("amy")
	assert.Equal(t, "1.4M", amyCell, "a refused note changes nothing")
}

func TestParseResultUsage(t *testing.T) {
	t.Parallel()
	u, ok := sprint.ParseResultUsage("hello\nusage: in=1 out=2 cache=3 usd=1.231\nusage: in=9 out=9 cache=9\n")
	require.True(t, ok)
	assert.Equal(t, sprint.ResultUsage{In: 1, Out: 2, Cache: 3, USD: "1.231"}, u, "the first usage line wins")

	u, ok = sprint.ParseResultUsage("**usage:** cache=5 in=3 out=4\n")
	require.True(t, ok)
	assert.Equal(t, sprint.ResultUsage{In: 3, Out: 4, Cache: 5}, u)

	_, ok = sprint.ParseResultUsage("no line\n")
	assert.False(t, ok)
	_, ok = sprint.ParseResultUsage("usage: in=-1 out=nope cache=1.5\n")
	assert.False(t, ok, "a negative, a word and a fraction are not counts")
	_, ok = sprint.ParseResultUsage("misusage: in=1 out=2 cache=3\n")
	assert.False(t, ok)

	u, ok = sprint.ParseResultUsage("usage: usd=1.5\n")
	require.True(t, ok)
	assert.Equal(t, sprint.ResultUsage{USD: "1.5"}, u)
	u, ok = sprint.ParseResultUsage("usage: in=1 usd=nope\n")
	require.True(t, ok)
	assert.Equal(t, sprint.ResultUsage{In: 1}, u, "a usd that is not a decimal is skipped")
}

func TestFriendTokensCellShowsCompactCountsOrDollarsRoundedUp(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0", sprint.CompactTokens(0))
	assert.Equal(t, "999", sprint.CompactTokens(999))
	assert.Equal(t, "1K", sprint.CompactTokens(1000))
	assert.Equal(t, "1.2M", sprint.CompactTokens(1_200_000))
	assert.Equal(t, "2M", sprint.CompactTokens(2_000_000))
	assert.Equal(t, "1.3M", sprint.CompactTokens(1_250_000), "one decimal, half up")

	assert.Equal(t, "0", sprint.FriendTokensCell(sprint.ResultUsage{}, ""))
	assert.Equal(t, "1.2M", sprint.FriendTokensCell(sprint.ResultUsage{In: 1_200_000, USD: "9.99"}, ""))
	assert.Equal(t, "1.2M", sprint.FriendTokensCell(sprint.ResultUsage{In: 1_200_000, USD: "9.99"}, "subscription"))
	assert.Equal(t, "1.2M", sprint.FriendTokensCell(sprint.ResultUsage{In: 1_200_000, USD: "9.99"}, "plan"))

	assert.Equal(t, "$1.24", sprint.FriendTokensCell(sprint.ResultUsage{USD: "1.231"}, "api"))
	assert.Equal(t, "$1.23", sprint.FriendTokensCell(sprint.ResultUsage{USD: "1.230"}, "metered"))
	assert.Equal(t, "$0.01", sprint.FriendTokensCell(sprint.ResultUsage{USD: "0.001"}, "API"))
	assert.Equal(t, "$0.00", sprint.FriendTokensCell(sprint.ResultUsage{In: 1_200_000}, "api"), "api with no usd shows zero dollars")

	sum := sprint.SumResultUsage([]sprint.ResultUsage{{In: 1, USD: "1.231"}, {In: 2, USD: "0.001"}, {USD: "nope"}})
	assert.Equal(t, sprint.ResultUsage{In: 3, USD: "1.232"}, sum)
	assert.Equal(t, "$1.24", sprint.FriendTokensCell(sum, "api"))
}
