package main

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// streamRows is each stream table's rows, as where shows them.
func (ta *testApp) streamRows() map[string][]string {
	ta.t.Helper()
	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	out := map[string][]string{}
	for _, t := range []string{sprint.Work, sprint.Merge} {
		rows := []string{}
		for r := range v.Tables[t] {
			rows = append(rows, r)
		}
		sort.Strings(rows)
		out[t] = rows
	}
	return out
}

// stream remove end to end on the twin (the owner, 2026-10-01: "remove work
// streams a/b/c" / "they should only succeed on a STOPPED sprint machine"):
// refused on a RUNNING machine and for a stream holding a card, all or none,
// nothing changed; on a STOPPED machine the rows leave the work and merge
// tables, a clear does not bring them back, and the name is added fresh in
// the next epoch.
func TestStreamRemoveTakesStreamsOffAStoppedSprint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, st := range []string{"a", "b", "c"} {
		ta.ok("add --stream " + st + " --count 1 --one")
	}
	ta.ok("clear --confirm sprint")
	all := map[string][]string{sprint.Work: {"a", "b", "c"}, sprint.Merge: {"a", "b", "c"}}
	require.Equal(t, all, ta.streamRows(), "a clear keeps the streams' rows")

	ta.ok("start")
	code, _, errs := ta.do("stream remove a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "the machine is RUNNING")
	assert.Contains(t, errs, "run: nova-sprint stop")
	assert.Equal(t, all, ta.streamRows(), "refused on RUNNING: nothing changed")
	ta.ok("stop --reason r --until 9999h")

	ta.ok("add --stream c --count 1 --one")
	before := ta.applies()
	code, _, errs = ta.do("stream remove a b c")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "c: stream c holds 1 primary;")
	assert.Contains(t, errs, "a: not removed: the verb names several and removes all or none")
	assert.Equal(t, all, ta.streamRows(), "all or none: nothing changed")
	assert.Equal(t, before, ta.applies())

	code, _, errs = ta.do("stream remove zz")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no stream zz on the work or merge table")

	out := ta.ok("stream remove a b")
	assert.Contains(t, out, "STREAM-REMOVE OK streams=a,b")
	assert.Equal(t, map[string][]string{sprint.Work: {"c"}, sprint.Merge: {"c"}}, ta.streamRows())
	ta.clean()

	// the control card the merge row took with it is never placed again in
	// this epoch: add refuses the name until the next clear, nothing changed
	code, _, errs = ta.do("add --stream a --count 1 --one")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "stream a was removed in this epoch")
	assert.Equal(t, map[string][]string{sprint.Work: {"c"}, sprint.Merge: {"c"}}, ta.streamRows())

	ta.ok("clear --confirm sprint")
	assert.Equal(t, map[string][]string{sprint.Work: {"c"}, sprint.Merge: {"c"}}, ta.streamRows(), "a clear does not bring a removed stream back")
	assert.Contains(t, ta.ok("add --stream a --count 1 --one"), "MOVED a-1 -> ready stream=a")
	assert.Equal(t, map[string][]string{sprint.Work: {"a", "c"}, sprint.Merge: {"a", "c"}}, ta.streamRows())
	ta.clean()
}

// The coordinator view of 2026-10-06 00:11 held six "stream stopped" judgments of
// streams stream remove had taken off: each next step was refused "no such stream".
// Removing a stream retires every open judgment that names it, a NOTE line each, and
// the view is without it.
func TestStreamRemoveRetiresTheStreamsJudgments(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.toMerging("s1", "s2")
	ta.ok("merge --stream s2 --red --suspect s2-1")
	g := ta.group(sprint.NRed, "s2")
	_, ok := item(ta.coordView(""), "j:"+g.ID)
	require.True(t, ok, "the view shows the judgment before")
	ta.ok("stop --reason r --until 9999h")
	ta.ok("drop s2-1 s2-2 s2-3 --reason obsolete")

	out := ta.ok("stream remove s2")
	assert.Contains(t, out, "STREAM-REMOVE OK streams=s2")
	assert.Contains(t, out, "NOTE judgment ")
	assert.Contains(t, out, g.ID+" ("+sprint.NRed+") retired: stream s2 was removed")
	v := ta.coordView("")
	_, ok = item(v, "j:"+g.ID)
	assert.False(t, ok, "the view without it")
	for _, it := range v.Items {
		assert.NotContains(t, it.Next, "--stream s2", "no item names s2: %+v", it)
	}
	ta.clean()
}
