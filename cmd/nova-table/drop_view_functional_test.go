//go:build functional

package main

// A drop names every view that names the table, each of which refuses to
// render without it, with the command that shows the view (USE defect 4: the
// drop said nothing, and render --view refused later). The Lua is T.delete in
// the nova_sprint library; the words are cmdDrop's.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestADropNamesTheViewsThatNameTheTable(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", args, code, out, errout)
		return out
	}
	success("create", "jobs", "--columns", "a,b")
	success("create", "tmp1", "--columns", "a,b")
	success("create", "tmp2", "--columns", "a")
	success("view", "set", "v1", "--tables", "tmp1")
	success("view", "set", "v2", "--tables", "jobs,tmp1")
	success("view", "set", "v3", "--tables", "jobs")
	out := success("drop", "tmp1")
	assert.Contains(t, out, "TABLE DROP table=tmp1 rows=0 trips=1\n", "drop tmp1")
	assert.Contains(t, out, "NOTE view v1 names the dropped table tmp1 and refuses to render until it is set without it or deleted; run: nova-table view show v1\n", "drop tmp1")
	assert.Contains(t, out, "NOTE view v2 names the dropped table tmp1", "drop tmp1")
	assert.NotContains(t, out, "view v3", "drop tmp1 names a view that does not name it")
	assert.NotContains(t, success("drop", "tmp2", "--definition"), "NOTE", "a table no view names")
}
