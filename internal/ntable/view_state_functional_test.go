//go:build functional

package ntable_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestAViewsStateIsItsSummaryLineAlone: ns_view_state sets and clears a view's
// state; while set, the summary line is the state alone; a view set leaves it
// as it is; a view that is not there is refused ErrNoView and nothing is
// written; a state that is not one short line is refused by the store; and the
// state can be written in the same MULTI/EXEC as a record of the caller's.
func TestAViewsStateIsItsSummaryLineAlone(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("build").cell("build", "done", "a", 1).cell("build", "ready", "b", 1)
	def := ntable.View{Name: "v", Tables: []string{"demo"}, Title: "T", Summary: "done"}
	require.NoError(t, ntable.ViewSet(ctx, c, def))
	line := func() string {
		t.Helper()
		v, err := ntable.ViewGet(ctx, c, "v")
		require.NoError(t, err)
		tb, err := ntable.Read(ctx, c, v.Tables[0])
		require.NoError(t, err)
		return ntable.SummaryLine(v, tb)
	}
	got := line()
	require.Equal(t, "1/2 50.0% -> ETA", got, "no state: %q", got)
	require.NoError(t, ntable.ViewState(ctx, c, "v", "STOPPED"))
	got = line()
	require.Equal(t, "STOPPED", got, "with a state: %q, want STOPPED alone", got)
	require.NoError(t, ntable.ViewSet(ctx, c, def))
	got = line()
	require.Equal(t, "STOPPED", got, "a view set dropped the state: %q", got)
	require.NoError(t, ntable.ViewState(ctx, c, "v", ""))
	got = line()
	require.Equal(t, "1/2 50.0% -> ETA", got, "cleared: %q", got)
	stored, err := c.HExists(ctx, "view:v", "state").Result()
	require.NoError(t, err)
	require.False(t, stored, "a cleared state is still stored")

	// A view that is not there: refused, and nothing is created.
	require.ErrorIs(t, ntable.ViewState(ctx, c, "gone", "STOPPED"), ntable.ErrNoView, "missing view")
	n, err := c.Exists(ctx, "view:gone").Result()
	require.NoError(t, err)
	require.Zero(t, n, "a refused state created the view")
	// The store refuses a state that is not one short line, whoever calls.
	for _, bad := range []string{"two\nlines", strings.Repeat("x", ntable.MaxViewState+1)} {
		reply, err := c.FCall(ctx, "ns_view_state", []string{"view:v"}, "v", bad).Slice()
		require.True(t, replyOpens(reply, err, "REFUSED", "ARGS"), "state %q: %v %v", bad, reply, err)
		require.False(t, ntable.ValidViewState(bad), "state %q passes the client's check", bad)
	}

	// In one transaction with a record of the caller's: both are written.
	var shown *redis.Cmd
	_, err = c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, "record", "stopped", 0)
		shown = ntable.QueueViewState(ctx, p, "v", "STOPPED")
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, ntable.ViewStateResult("v", shown))
	require.Equal(t, "STOPPED", line(), "transaction: line")
	require.Equal(t, "stopped", c.Get(ctx, "record").Val(), "transaction: record")
	var missing *redis.Cmd
	_, err = c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		missing = ntable.QueueViewState(ctx, p, "gone", "STOPPED")
		return nil
	})
	require.NoError(t, err)
	require.ErrorIs(t, ntable.ViewStateResult("gone", missing), ntable.ErrNoView, "queued on a missing view")
}
