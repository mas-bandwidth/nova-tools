package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// --answers with a stale id the machine answered already is a note on stdout,
// not a refusal of the whole step (the comfort list of 2026-10-03, item 11); a
// stale id another verb answered is still refused with the existing line.
func TestAnswersStaleIdTheMachineAnsweredIsANote(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 --count 1")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("take --as m1 s2-1.w1@1")
	// past its deadline unfinished: the tick's lateness judgment, raised by the machine
	ta.mu.Lock()
	ta.now = ta.now.Add(sprint.DeadlineUnfinished + time.Minute)
	ta.mu.Unlock()
	ta.ok("tick")
	late := ta.group(sprint.NWorkLate, "s1")
	require.Len(t, late.Notes, 1, "the lateness judgment: %+v", late)
	id := late.Notes[0]
	// the card finishes: the next tick finds its attempt no longer late and answers the
	// judgment itself (Who: the machine)
	ta.ok("finish --as m1 s1-1.w1@1 --head 9f3c2e1 --report 'tests green'")
	ta.ok("tick")
	for _, g := range ta.inboxGroups() {
		require.NotEqual(t, id, g.ID, "the machine did not answer %s: %+v", id, g)
	}

	code, out, errs := ta.do("drop s1-1 --reason obsolete --answers " + id)
	assert.Equal(t, 0, code, "%s%s", out, errs)
	assert.Contains(t, out, "MOVED s1-1")
	assert.Contains(t, out, "NOTE --answers "+id+": the machine answered it already; nothing more to answer")
	assert.Empty(t, errs)

	// a judgment the coordinator answered with another verb stays refused as before
	ta.ok("finish --as m1 s2-1.w1@1 --failed --report 'tests red'")
	ta.ok("tick")
	failed := ta.group(sprint.NWorkFailed, "s2")
	require.Len(t, failed.Notes, 1)
	ta.ok("rework s2-1 --fix 'the fix' --answers " + failed.Notes[0])
	code, out, errs = ta.do("drop s2-1 --reason obsolete --answers " + failed.Notes[0])
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, "no open judgment "+failed.Notes[0]+"; run: nova-sprint inbox; the whole step is refused and nothing was changed")
}
