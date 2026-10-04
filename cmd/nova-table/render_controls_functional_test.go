//go:build functional

package main

import (
	"context"
	"github.com/redis/go-redis/v9"

	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/require"
)

func TestRenderAndWatchEscapeStoredControls(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	raw := "before\x1b[2Jafter\nsecond"
	for _, args := range [][]string{
		{"create", "t", "--columns", "a:count:sum:" + raw + ",note:text", "--footer", raw},
		{"row", "add", "t", "r", "--label", raw},
		{"row", "set", "t", "r", "note=" + raw},
		{"view", "set", "v", "--tables", "t", "--title", raw},
	} {
		{
			code, out, errout := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "setup %v: %d %s %s", args, code, out, errout)
		}
	}
	for _, args := range [][]string{{"render", "t"}, {"render", "--view", "v"}, {"watch", "--view", "v", "--once"}, {"watch", "t", "--title", raw, "--once"}} {
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", args, code, out, errout)
		require.Empty(t, errout, "%v: %d %q %q", args, code, out, errout)
		require.NotContains(t, out, "\x1b", "%v: %d %q %q", args, code, out, errout)
		require.NotContains(t, out, "after\nsecond", "%v: %d %q %q", args, code, out, errout)
		require.Contains(t, out, `before\x1b[2Jafter\x0asecond`, "%v: %d %q %q", args, code, out, errout)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tab, err := ntable.Read(context.Background(), c, "t")
	require.NoError(t, err, "display changed stored data: %+v %v", tab, err)
	require.Equal(t, raw, tab.Rows[0].Texts["note"], "display changed stored data: %+v %v", tab, err)
	require.Equal(t, raw, tab.Rows[0].Label, "display changed stored data: %+v %v", tab, err)
	require.Equal(t, raw, tab.FooterLabel, "display changed stored data: %+v %v", tab, err)
	require.Equal(t, raw, tab.Columns[0].Label, "display changed stored data: %+v %v", tab, err)
}
