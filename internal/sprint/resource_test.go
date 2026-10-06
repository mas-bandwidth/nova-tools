package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resources table's rules, on the pure table (resource.go; the model is
// tla/Resources.tla, whose invariants these names follow).

var rT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func table(t *testing.T, capacity int) Resources {
	t.Helper()
	rs, err := Resources{}.Add("bench-a", "bench", capacity)
	require.NoError(t, err)
	return rs
}

func TestResourceAddRefusesWhatItMust(t *testing.T) {
	t.Parallel()
	_, err := Resources{}.Add("bad name", "bench", 1)
	assert.ErrorContains(t, err, "letters, digits")
	_, err = Resources{}.Add("x", "cloud", 1)
	assert.ErrorContains(t, err, "--kind is one of bench, branch, port, account")
	_, err = Resources{}.Add("x", "", 1)
	assert.ErrorContains(t, err, "--kind wants one of")
	_, err = Resources{}.Add("x", "port", 0)
	assert.ErrorContains(t, err, "--capacity wants a count from 1")
	rs := table(t, 1)
	_, err = rs.Add("bench-a", "bench", 2)
	assert.ErrorContains(t, err, "is on the table")
	rs, ch, err := rs.SetCapacity("bench-a", 2, rT0)
	require.NoError(t, err)
	assert.Equal(t, 2, rs["bench-a"].Capacity)
	assert.Empty(t, ch.Granted)
}

// CapacityKept: never more holders than the capacity, whatever is claimed.
func TestResourceCapacityIsNeverExceeded(t *testing.T) {
	t.Parallel()
	rs := table(t, 2)
	var err error
	var ans ResourceAnswer
	for i, who := range []string{"a", "b", "c", "d"} {
		rs, ans, err = rs.Claim("bench-a", who, time.Hour, rT0.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		assert.LessOrEqual(t, len(rs["bench-a"].Holders), 2, "after %s", who)
		if i < 2 {
			assert.True(t, ans.Granted, who)
			assert.Equal(t, rT0.Add(time.Duration(i)*time.Second).Add(time.Hour), ans.Until)
		} else {
			assert.False(t, ans.Granted, who)
			assert.Equal(t, i-1, ans.Place, who)
		}
	}
	// a claim again by a waiter keeps its place; by a holder renews its lease
	rs, ans, err = rs.Claim("bench-a", "d", 2*time.Hour, rT0.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 2, ans.Place)
	rs, ans, err = rs.Claim("bench-a", "a", 2*time.Hour, rT0.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, ans.Granted)
	assert.Equal(t, rT0.Add(time.Minute).Add(2*time.Hour), ans.Until)
	assert.Equal(t, 2, len(rs["bench-a"].Holders))
}

// NoRoomWasted and NoOvertaking: a release grants the head of the line, never a
// later waiter, and nobody waits while a place is free.
func TestResourceReleaseGrantsTheHeadOfTheLine(t *testing.T) {
	t.Parallel()
	rs := table(t, 1)
	var err error
	for _, who := range []string{"a", "b", "c"} {
		rs, _, err = rs.Claim("bench-a", who, time.Hour, rT0)
		require.NoError(t, err)
	}
	rs, ans, err := rs.Release("bench-a", "a", rT0.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, ans.Gave)
	assert.Equal(t, []string{"b"}, ans.GrantedNow)
	assert.Equal(t, []string{"b"}, rs["bench-a"].holders())
	assert.Equal(t, []string{"c"}, rs["bench-a"].waiters())
	assert.Equal(t, rT0.Add(time.Minute).Add(time.Hour), rs["bench-a"].Holders[0].Until, "the lease runs from the grant")
	// a waiter leaving the line
	rs, ans, err = rs.Release("bench-a", "c", rT0.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, ans.Gave)
	assert.Empty(t, rs["bench-a"].Waiters)
	// a release by a stranger gives nothing and is not an error
	_, ans, err = rs.Release("bench-a", "z", rT0)
	require.NoError(t, err)
	assert.False(t, ans.Gave)
}

func TestResourceRenewIsAHoldersAlone(t *testing.T) {
	t.Parallel()
	rs := table(t, 1)
	rs, _, err := rs.Claim("bench-a", "a", time.Hour, rT0)
	require.NoError(t, err)
	rs, _, err = rs.Claim("bench-a", "b", time.Hour, rT0)
	require.NoError(t, err)
	rs, ans, err := rs.Renew("bench-a", "a", 3*time.Hour, rT0.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, ans.Granted)
	assert.Equal(t, rT0.Add(time.Minute).Add(3*time.Hour), ans.Until)
	_, _, err = rs.Renew("bench-a", "b", time.Hour, rT0)
	assert.ErrorContains(t, err, "holds no lease of bench-a: it is 1st in the line")
	_, _, err = rs.Renew("bench-a", "z", time.Hour, rT0)
	assert.ErrorContains(t, err, "holds no lease of bench-a; claim one")
	_, _, err = rs.Renew("bench-a", "a", 0, rT0)
	assert.ErrorContains(t, err, "--for wants the lease's length")
	_, _, err = rs.Renew("bench-a", "a", 25*time.Hour, rT0)
	assert.ErrorContains(t, err, "at most 24h0m0s")
	_, _, err = rs.Claim("nosuch", "a", time.Hour, rT0)
	assert.ErrorContains(t, err, "no resource nosuch on the table")
}

// The tick: an expired lease is released and the head granted; a holder down is
// released at once (DownHoldsNothing); a waiter down leaves the line; a line with
// no room past the bound is starving.
func TestResourceTickReleasesExpiredAndDownAndServesTheLine(t *testing.T) {
	t.Parallel()
	rs := table(t, 1)
	var err error
	rs, _, err = rs.Claim("bench-a", "a", time.Hour, rT0)
	require.NoError(t, err)
	rs, _, err = rs.Claim("bench-a", "b", 30*time.Minute, rT0)
	require.NoError(t, err)
	rs, _, err = rs.Claim("bench-a", "c", time.Hour, rT0)
	require.NoError(t, err)
	// nothing due: no change; the starvation clock runs from b's claim
	assert.Equal(t, rT0, rs["bench-a"].Starved)
	rs, changes := rs.Tick(rT0.Add(time.Minute), nil)
	assert.Empty(t, changes, "nothing due is no change")
	rs, changes = rs.Tick(rT0.Add(2*time.Minute), nil)
	assert.Empty(t, changes, "the same state again is no change")
	// the holder expires: b, the head, is granted for its own length
	rs, changes = rs.Tick(rT0.Add(time.Hour), nil)
	require.Len(t, changes, 1)
	assert.Equal(t, []string{"a"}, changes[0].Expired)
	assert.Equal(t, []string{"b"}, changes[0].Granted)
	assert.Equal(t, []string{"b"}, rs["bench-a"].holders())
	assert.Equal(t, rT0.Add(time.Hour).Add(30*time.Minute), rs["bench-a"].Holders[0].Until)
	assert.Equal(t, rT0, rs["bench-a"].Starved, "the line still has no room: the clock runs on")
	// the holder goes down: released at once, c granted
	down := func(who string) bool { return who == "b" }
	rs, changes = rs.Tick(rT0.Add(time.Hour+time.Minute), down)
	require.Len(t, changes, 1)
	assert.Equal(t, []string{"b"}, changes[0].Down)
	assert.Equal(t, []string{"c"}, changes[0].Granted)
	assert.Equal(t, []string{"c"}, rs["bench-a"].holders())
	assert.True(t, rs["bench-a"].Starved.IsZero(), "nobody waits: the clock stops")
	for _, h := range rs["bench-a"].Holders {
		assert.False(t, down(h.Who), "a member down holds nothing")
	}
	// a waiter down leaves the line
	rs, _, err = rs.Claim("bench-a", "d", time.Hour, rT0.Add(time.Hour+time.Minute))
	require.NoError(t, err)
	rs, changes = rs.Tick(rT0.Add(time.Hour+2*time.Minute), func(who string) bool { return who == "d" })
	require.Len(t, changes, 1)
	assert.Equal(t, []string{"d"}, changes[0].Left)
	assert.Empty(t, rs["bench-a"].Waiters)
}

func TestResourceStarvingIsNamedPastTheBound(t *testing.T) {
	t.Parallel()
	rs := table(t, 1)
	var err error
	rs, _, err = rs.Claim("bench-a", "a", 2*time.Hour, rT0)
	require.NoError(t, err)
	rs, _, err = rs.Claim("bench-a", "b", time.Hour, rT0)
	require.NoError(t, err)
	assert.Equal(t, rT0, rs["bench-a"].Starved, "the clock runs from the claim that was queued")
	rs, changes := rs.Tick(rT0.Add(time.Second), nil)
	assert.Empty(t, changes)
	rs, changes = rs.Tick(rT0.Add(ResourceStarveBound-time.Second), nil)
	assert.Empty(t, changes, "under the bound: nothing to say")
	rs, changes = rs.Tick(rT0.Add(ResourceStarveBound), nil)
	require.Len(t, changes, 1)
	assert.True(t, changes[0].Starving)
	_, changes = rs.Tick(rT0.Add(ResourceStarveBound+time.Minute), nil)
	require.Len(t, changes, 1, "starving is said at every tick while it stands; the store raises one judgment")
	assert.True(t, changes[0].Starving)
}

func TestResourceRowsAndLines(t *testing.T) {
	t.Parallel()
	rs := table(t, 1)
	rs, _, err := rs.Claim("bench-a", "a", time.Hour, rT0)
	require.NoError(t, err)
	rs, _, err = rs.Claim("bench-a", "b", time.Hour, rT0)
	require.NoError(t, err)
	rs, err = rs.Add("dev-branch", "branch", 1)
	require.NoError(t, err)
	rows := rs.Rows()
	require.Len(t, rows, 2)
	assert.Equal(t, "dev-branch", rows[1].Name, "by name")
	assert.NotNil(t, rows[1].Holders)
	assert.NotNil(t, rows[1].Waiters)
	assert.Equal(t, "RESOURCE dev-branch kind=branch held=0/1 holders: nobody; waiting: nobody", rows[1].Line(rT0))
	line := rows[0].Line(rT0.Add(time.Minute))
	assert.Contains(t, line, "RESOURCE bench-a kind=bench held=1/1 holders: a (until 2030-01-02T04:04:05Z, 59m0s left); waiting: 1. b (for 1h0m0s, since 2030-01-02T03:04:05Z)")
	assert.Contains(t, rows[0].Line(rT0.Add(2*time.Hour)), "a (expired 1h0m0s ago)")
	_, err = rs.Remove("bench-a")
	assert.ErrorContains(t, err, "is held by a and waited for by b")
	rs, err = rs.Remove("dev-branch")
	require.NoError(t, err)
	assert.Equal(t, []string{"bench-a"}, rs.Names())
}
