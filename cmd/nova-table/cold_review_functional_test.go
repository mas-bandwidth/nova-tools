//go:build functional

package main

import (
	"context"
	"github.com/redis/go-redis/v9"

	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
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

// TestTheHelpPastesTheStaleRefusalItPrints holds the banner's pasted refusal
// line and the epoch's two sources to what the tool prints: the line the tool
// prints and the line the help shows for it are the same text, byte for byte
// (docs/STANDARD.md section 2), show prints the live epoch, the receipt prints
// the epoch the write committed, a second cell add of the same member is
// refused naming where it sits, and a second row add rewrites the row and
// keeps its place.
func TestTheHelpPastesTheStaleRefusalItPrints(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	code, banner, errout := runTable("help")
	require.EqualValues(t, 0, code, "help: %d %q", code, errout)
	require.Empty(t, errout, "help: %d %q", code, errout)
	for _, args := range [][]string{
		{"create", "stale-help", "--columns", "ready,working,done", "--epoch-key", "stale-help:epoch"},
		{"row", "add", "stale-help", "build"},
	} {
		code, out, errs := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %s %s", args, code, out, errs)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	// one write in, the table's epoch key says 1, and the next write at the
	// default --epoch 0 is stale
	require.NoError(t, c.HSet(ctx, "stale-help:epoch", "n", 1).Err())
	var out string
	code, out, errout = runTable(at(addr, "cell", "add", "stale-help", "build", "ready", "b1")...)
	require.EqualValues(t, 1, code, "stale cell add: %d %s %q", code, out, errout)
	got, _, ok := strings.Cut(errout, "\n")
	require.True(t, ok, "one refusal line: %q", errout)
	// the banner indents the lines it quotes by two spaces, as it indents
	// every command line
	require.Contains(t, banner, "  "+got+"\n", "the banner does not paste the refusal the tool prints:\n  tool: %q\n", got)
	code, out, errout = runTable(at(addr, "show", "stale-help")...)
	require.EqualValues(t, 0, code, "show: %d %s %q", code, out, errout)
	require.Contains(t, out, " epoch=1 ", "show prints the live epoch: %s", out)
	// the epoch moved under the table, so the row the first epoch made is not
	// the live one: place it at epoch 1 before a cell goes into it
	code, out, errout = runTable(at(addr, "row", "add", "stale-help", "build", "--epoch", "1")...)
	require.EqualValues(t, 0, code, "row add at the live epoch: %d %s %q", code, out, errout)
	code, out, errout = runTable(at(addr, "cell", "add", "stale-help", "build", "working", "b2", "--epoch", "1", "--receipt")...)
	require.EqualValues(t, 0, code, "receipt write: %d %s %q", code, out, errout)
	require.Contains(t, out, "TABLE RECEIPT event=", "the receipt of every write prints the new one: %s", out)
	require.Contains(t, out, " epoch=1 ", "the receipt of every write prints the new one: %s", out)
	// The stale probe above is refused before any staged write runs, so b1 sits
	// nowhere yet: place it first, then the identical add is a second one.
	code, out, errout = runTable(at(addr, "cell", "add", "stale-help", "build", "ready", "b1", "--epoch", "1")...)
	require.EqualValues(t, 0, code, "first cell add: %d %s %q", code, out, errout)
	code, out, errout = runTable(at(addr, "cell", "add", "stale-help", "build", "ready", "b1", "--epoch", "1")...)
	require.EqualValues(t, 1, code, "second cell add: %d %s %q", code, out, errout)
	// The store's place detail is the function's reply slice (T.place joins row
	// and column with a colon), and Go prints a slice with brackets.
	require.Contains(t, errout, "member already has a place in this table: [build:ready b1]",
		"a second cell add of the same member is refused, naming the place it already sits: %q", errout)
	code, out, errout = runTable(at(addr, "row", "add", "stale-help", "other", "--epoch", "1")...)
	require.EqualValues(t, 0, code, "row add other: %d %s %q", code, out, errout)
	code, out, errout = runTable(at(addr, "row", "add", "stale-help", "build", "--epoch", "1")...)
	require.EqualValues(t, 0, code, "second row add: %d %s %q", code, out, errout)
	code, out, errout = runTable(at(addr, "show", "stale-help")...)
	require.EqualValues(t, 0, code, "show order: %d %s %q", code, out, errout)
	require.Less(t, strings.Index(out, "build"), strings.Index(out, "other"),
		"a second row add rewrites the row and keeps its place: %s", out)
}
