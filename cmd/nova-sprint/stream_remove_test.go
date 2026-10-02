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
		ta.ok("add --stream " + st + " --count 1")
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
	ta.ok("stop")

	ta.ok("add --stream c --count 1")
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
	code, _, errs = ta.do("add --stream a --count 1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "stream a was removed in this epoch")
	assert.Equal(t, map[string][]string{sprint.Work: {"c"}, sprint.Merge: {"c"}}, ta.streamRows())

	ta.ok("clear --confirm sprint")
	assert.Equal(t, map[string][]string{sprint.Work: {"c"}, sprint.Merge: {"c"}}, ta.streamRows(), "a clear does not bring a removed stream back")
	assert.Contains(t, ta.ok("add --stream a --count 1"), "MOVED a-1 -> ready stream=a")
	assert.Equal(t, map[string][]string{sprint.Work: {"a", "c"}, sprint.Merge: {"a", "c"}}, ta.streamRows())
	ta.clean()
}
