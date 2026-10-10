//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The stored view shows a stream with no cards in any column: after init, an
// add of three streams and a clear, the drawn view still has the work and merge
// tables with their three stream rows at zero, and the readers and the fleet. The
// frame is the one `nova-table watch --view sprint` draws (ntable.RenderTables).
func TestTheViewShowsAStreamWithNoCards(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, fn.Load(ctx, c))
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	run := func(args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		require.Equal(t, 0, code, "%v: %d %s", args, code, errb.String())
	}
	frame := func() string {
		t.Helper()
		v, err := ntable.ViewGet(ctx, c, "sprint")
		require.NoError(t, err)
		var tables []ntable.Table
		for _, name := range v.Tables {
			tb, err := ntable.Read(ctx, c, name)
			require.NoError(t, err)
			tables = append(tables, tb)
		}
		return ntable.RenderTables("", tables, ntable.RenderOpts{})
	}
	has := func(text, table string) bool {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, table+" ") || line == table {
				return true
			}
		}
		return false
	}

	run("init", "--readers", "reader-a,reader-b", "--members", "m1")
	run("add", "--stream", "a,b,c", "--count", "1", "--one")
	got := frame()
	require.True(t, has(got, "work"), "streams with a card each: the work table is drawn:\n%s", got)
	require.Contains(t, got, "\na ", "streams with a card each: the work table is drawn:\n%s", got)
	run("clear", "--confirm", "sprint")
	got = frame()
	for _, table := range []string{"work", "merge", "readers", "fleet"} {
		require.True(t, has(got, table), "after a clear the %s table is drawn:\n%s", table, got)
	}
	for _, stream := range []string{"\na ", "\nb ", "\nc "} {
		require.GreaterOrEqual(t, strings.Count(got, stream), 2, "after a clear the stream row %q is in the work and merge tables, at zero:\n%s", stream, got)
	}
	run("add", "--stream", "b", "--count", "1", "--one")
	got = frame()
	require.True(t, has(got, "work"), "every stream is drawn, with a card or none:\n%s", got)
	require.Contains(t, got, "\na ", "every stream is drawn, with a card or none:\n%s", got)
	require.Contains(t, got, "\nb ", "every stream is drawn, with a card or none:\n%s", got)
	require.Contains(t, got, "\nc ", "every stream is drawn, with a card or none:\n%s", got)
}
