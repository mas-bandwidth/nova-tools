//go:build functional

package main

import (
	"context"
	"github.com/redis/go-redis/v9"

	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColumnRemovalNamesAllBlockersAndRunnableBatches(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	for _, args := range [][]string{{"create", "t", "--columns", "active,done"}, {"row", "add", "t", "first row", "second"}, {"cell", "add", "t", "first row", "active", "m1", "m2"}} {
		{
			code, out, errout := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "%v: %d %s %s", args, code, out, errout)
		}
	}
	{
		code, out, errout := runTable("cell", "add", "t", "second", "active", "--redis", addr, "--", "--third")
		require.EqualValues(t, 0, code, "flag-like member: %d %s %s", code, out, errout)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	before, err := ntable.Read(ctx, c, "t")
	require.NoError(t, err, "%v", err)
	code, out, errout := runTable(at(addr, "col", "del", "t", "active")...)
	require.EqualValues(t, 1, code, "occupied delete: %d %s %s", code, out, errout)
	require.Empty(t, out, "occupied delete: %d %s %s", code, out, errout)
	for _, want := range []string{`row "first row"`, `row "second"`, `"m1"`, `"m2"`, `"--third"`, `cell remove 't' 'first row' 'active' 'm1' 'm2'`, `cell remove -- 't' 'second' 'active' '--third'`} {
		assert.Contains(t, errout, want, "missing blocker/remedy %q in %s", want, errout)
	}
	after, err := ntable.Read(ctx, c, "t")
	require.NoError(t, err, "refused deletion changed snapshot: %v", err)
	require.Equal(t, after, before, "refused deletion changed snapshot: %v", err)
	_, remedies, ok := strings.Cut(strings.TrimSpace(errout), "; run: ")
	require.True(t, ok, "%v", errout)
	for _, line := range strings.Split(remedies, "; ") {
		words, err := onboarding.SplitShell(line)
		require.NoError(t, err, "%v", err)
		// The advertised command, with this fixture's address supplied before
		// any -- separator. Both remedies remove a whole cell's blockers.
		args := append([]string{"cell", "remove", "--redis", addr}, words[3:]...)
		{
			code, out, errout := runTable(args...)
			require.EqualValues(t, 0, code, "remedy %q: %d %s %s", line, code, out, errout)
		}
	}
	{
		code, out, errout := runTable(at(addr, "col", "del", "t", "active")...)
		require.EqualValues(t, 0, code, "cleared column: %d %s %s", code, out, errout)
	}
}

func TestRenderStoredViewIsDiscoverableOneFrame(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	for _, args := range [][]string{{"create", "t", "--columns", "ready,done"}, {"row", "add", "t", "r"}, {"cell", "add", "t", "r", "done", "m"}, {"view", "set", "v", "--tables", "t", "--title", "Working view", "--summary", "done"}} {
		{
			code, out, errout := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "%v: %d %s %s", args, code, out, errout)
		}
	}
	code, render, errout := runTable(at(addr, "render", "--view", "v", "--label-width", "10")...)
	require.EqualValues(t, 0, code, "render: %d %s %s", code, render, errout)
	require.Empty(t, errout, "render: %d %s %s", code, render, errout)
	require.Contains(t, render, "Working view\n\n1/1 100.0% -> ETA", "render: %d %s %s", code, render, errout)
	require.NotContains(t, render, clearScreen, "render: %d %s %s", code, render, errout)
	code, watch, errout := runTable(at(addr, "watch", "--view", "v", "--once", "--label-width", "10")...)
	_, renderBody, _ := strings.Cut(render, "\n")
	_, watchBody, _ := strings.Cut(watch, "\n")
	require.EqualValues(t, 0, code, "same view frame:\n%s\n%s\n%s", render, watch, errout)
	require.Empty(t, errout, "same view frame:\n%s\n%s\n%s", render, watch, errout)
	require.Equal(t, watchBody, renderBody, "same view frame:\n%s\n%s\n%s", render, watch, errout)
	for _, args := range [][]string{{"render", "t", "--view", "v"}, {"render", "--view", "v", "--at-epoch", "0"}} {
		{
			code, out, _ := runTable(at(addr, args...)...)
			require.EqualValues(t, 2, code, "invalid target combination: %v %d %s", args, code, out)
			require.Empty(t, out, "invalid target combination: %v %d %s", args, code, out)
		}
	}
	{
		code, out, errout := runTable(at(addr, "render", "--view", "missing")...)
		require.EqualValues(t, 1, code, "missing view: %d %s %s", code, out, errout)
		require.Empty(t, out, "missing view: %d %s %s", code, out, errout)
		require.Contains(t, errout, "view set", "missing view: %d %s %s", code, out, errout)
	}
}
