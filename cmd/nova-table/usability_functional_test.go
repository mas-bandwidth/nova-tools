//go:build functional

package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestWorkingStateAndViewLifecycle(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", args, code, out, errout)
		require.Empty(t, errout, "%v: %d %q %q", args, code, out, errout)
		return out
	}
	success("create", "work", "--columns", "todo,done,note:text,progress:pct(done)", "--footer", "total")
	success("row", "add", "work", "stream: build", "empty")
	success("cell", "add", "work", "stream: build", "todo", "check-a", "check-b")
	success("cell", "move", "work", "stream: build", "todo", "done", "check-a")
	success("row", "set", "work", "stream: build", "note=review passed")
	{
		out := success("set", "work", "--hide", "note")
		assert.Contains(t, out, `hide="note" trips=1`, "hide confirmation: %s", out)
	}
	{
		out := success("set", "work", "--show", "note")
		assert.Contains(t, out, `show="note" trips=1`, "show confirmation: %s", out)
	}

	out := success("show", "work")
	for _, part := range []string{`row="stream: build" todo=1 done=1 note="review passed" progress=50.0%`, `row=empty todo=0 done=0 note="" progress=0.0%`, `trips=1 epoch=0 revision=7`} {
		assert.Contains(t, out, part, "show missing %q: %s", part, out)
	}
	out = success("member", "find", "work", "check-a")
	assert.Contains(t, out, `state=placed row="stream: build" col=done epoch=0 table_revision=7 trips=1`, "find: %s", out)
	success("member", "create", "work", "new")
	{
		out = success("member", "find", "work", "new")
		assert.Contains(t, out, "state=unplaced epoch=0 table_revision=8 trips=1", "unplaced: %s", out)
	}
	{
		out = success("member", "find", "work", "unknown")
		assert.Contains(t, out, "state=missing epoch=0 table_revision=8 trips=1", "missing: %s", out)
	}
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	{
		out = success("view", "show", "today")
		assert.Contains(t, out, `title="My work" summary=done state="" trips=1`, "view show: %s", out)
	}
	{
		out = success("view", "list")
		assert.Equal(t, "VIEW LIST views=1 trips=1\nVIEW view=today\n", out, "list: %s", out)
	}
	success("watch", "--view", "today", "--once")
	for _, bad := range []struct {
		args []string
		want string
	}{
		{[]string{"view", "set", "bad", "--tables", "work", "--summary", "progress"}, `view "bad": summary wants a count column in table "work"; "progress" is not one; run: nova-table show 'work'`},
		{[]string{"watch", "--view", "missing", "--once"}, `view "missing": no such view; run: nova-table view set 'missing' --tables <a,b,...>`},
		{[]string{"view", "set", "bad", "--tables", "gone"}, `view "bad": referenced table "gone" does not exist; run: nova-table create 'gone' --columns <columns>`},
	} {
		code, out, errout := runTable(at(addr, bad.args...)...)
		assert.EqualValues(t, 1, code, "%v: %d %q %q", bad.args, code, out, errout)
		assert.Empty(t, out, "%v: %d %q %q", bad.args, code, out, errout)
		assert.Contains(t, errout, bad.want, "%v: %d %q %q", bad.args, code, out, errout)
		assert.EqualValues(t, 1, strings.Count(errout, "; run:"), "%v: %d %q %q", bad.args, code, out, errout)
	}
	{
		code, _, errout := runTable(at(addr, "watch", "work", "--view", "today", "--once")...)
		require.EqualValues(t, 2, code, "mixed watch targets: %d %s", code, errout)
		require.Contains(t, errout, "or --view", "mixed watch targets: %d %s", code, errout)
	}

	{
		out = success("view", "del", "today")
		assert.Equal(t, "VIEW DEL view=today existed=1 trips=1\n", out, "del: %s", out)
	}
	{
		out = success("view", "del", "today")
		assert.Equal(t, "VIEW DEL view=today existed=0 trips=1\n", out, "repeat del: %s", out)
	}
	{
		out = success("view", "list")
		assert.Equal(t, "VIEW LIST views=0 trips=1\n", out, "empty list: %s", out)
	}
	{
		out = success("check", "work")
		assert.Contains(t, out, "revision=8 members=2", "view operations changed table: %s", out)
	}
}

// TestViewStateVerb: view state sets the text the summary line shows alone,
// view show prints it, view set keeps it, --clear shows the counts again, and a
// missing view or a bad state is refused.
func TestViewStateVerb(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", args, code, out, errout)
		require.Empty(t, errout, "%v: %d %q %q", args, code, out, errout)
		return out
	}
	summary := func() string {
		t.Helper()
		out := success("render", "--view", "today")
		parts := strings.SplitN(out, "\n\n", 4) // clock, title, summary line, tables
		require.GreaterOrEqual(t, len(parts), 3, "frame: %q", out)
		return parts[2]
	}
	success("create", "work", "--columns", "todo,done")
	success("row", "add", "work", "build")
	success("cell", "add", "work", "build", "todo", "a", "b")
	success("cell", "move", "work", "build", "todo", "done", "a")
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	{
		got := summary()
		require.Equal(t, "1/2 50.0% -> ETA", got, "no state: %q", got)
	}
	{
		out := success("view", "state", "today", "STOPPED")
		require.Equal(t, "VIEW STATE view=today state=\"STOPPED\" trips=1\n", out, "view state: %q", out)
	}
	{
		got := summary()
		require.Equal(t, "STOPPED", got, "with a state: %q, want STOPPED alone", got)
	}
	{
		out := success("view", "show", "today")
		require.Contains(t, out, `summary=done state="STOPPED" trips=1`, "view show: %q", out)
	}
	success("view", "set", "today", "--tables", "work", "--summary", "done", "--title", "My work")
	{
		got := summary()
		require.Equal(t, "STOPPED", got, "view set dropped the state: %q", got)
	}
	success("view", "state", "today", "--clear")
	{
		got := summary()
		require.Equal(t, "1/2 50.0% -> ETA", got, "cleared: %q", got)
	}
	for _, bad := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"view", "state", "missing", "STOPPED"}, 1, `view "missing": no such view`},
		{[]string{"view", "state", "today"}, 2, "wants a view name and its state text"},
		{[]string{"view", "state", "today", "a\nb"}, 2, "a state is one line"},
	} {
		code, out, errout := runTable(at(addr, bad.args...)...)
		assert.Equal(t, bad.code, code, "%v: %d %q %q", bad.args, code, out, errout)
		assert.Empty(t, out, "%v: %d %q %q", bad.args, code, out, errout)
		assert.Contains(t, errout, bad.want, "%v: %d %q %q", bad.args, code, out, errout)
	}
}

// A drawn view shows every table and every row of it, all-zero or not; a
// table with no row shows its header and footer.
func TestViewDrawsEveryTableAndRow(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", args, code, out, errout)
		require.Empty(t, errout, "%v: %d %q %q", args, code, out, errout)
		return out
	}
	success("create", "work", "--columns", "todo,done")
	success("create", "bare", "--columns", "todo,done")
	success("row", "add", "work", "idle")
	success("view", "set", "v", "--tables", "work,bare")
	{
		out := success("view", "show", "v")
		assert.NotContains(t, out, "hide", "view show: %s", out)
	}
	out := success("watch", "--view", "v", "--once")
	for _, want := range []string{"\nidle ", "\nwork ", "\nbare "} {
		assert.Contains(t, out, want, "the frame lacks %q:\n%s", want, out)
	}
}
