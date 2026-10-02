package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Nothing a verb did that its reader needs to know happens silently:
//   - an add of cards with no brief says so, and how to give one (brief);
//   - a take by count that took fewer than asked says why (the member's width,
//     its empty ready queue, or its status);
//   - init echoes the readers it set;
//   - on a twin, init says when a member it adds is up, as the help does.
func TestAVerbSaysWhatTheMovesDoNot(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	init := ta.ok("init --readers reader-a,reader-b --members m1:2,m2")
	assert.Contains(t, init, "INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a,reader-b\n")
	assert.NotContains(t, init, "NOTE a twin", "a store that is not a twin")
	ta.ok("fleet down m2")

	add := ta.ok("add --stream s1 --count 5")
	assert.Contains(t, add, "ADD OK moved=5 refused=0 notes=0 op=")
	assert.Contains(t, add, "\nNOTE the cards have no brief, so a worker is handed no task with them; give each one before it is dealt, on a STOPPED machine: nova-sprint brief <id> --brief-file <path>\n")
	assert.NotContains(t, ta.ok("add --stream s2 --count 1 --brief-file "+writeBrief(t, "Fix it.")), "NOTE the cards have no brief")
	assert.NotContains(t, ta.ok("add --stream s1 --sentinel s1-gate"), "NOTE the cards have no brief", "a sentinel has no brief by design")

	ta.ok("start")
	ta.ok("tick")
	// m1 holds DealAhead times its width, and takes its width
	take := ta.ok("take --as m1 --limit 3")
	assert.Contains(t, take, "TAKE OK moved=2 ")
	assert.Contains(t, take, "\nNOTE m1 took 2 of the 3 asked: it is at its width, 2 working of 2; it takes another as it finishes one\n")
	assert.Contains(t, ta.ok("take --as m1"), "\nNOTE m1 took 0 of the 1 asked: it is at its width, 2 working of 2;")

	assert.Contains(t, ta.ok("take --as m2"), "\nNOTE m2 took 0 of the 1 asked: it is down, and only a member up takes\n")

	ta.ok("init --readers reader-c")
	assert.Contains(t, ta.ok("init"), "readers=reader-a,reader-b,reader-c\n", "init echoes every reader of the sprint")
}

// A take that took all it asked says nothing more, and a member whose ready
// queue ran out says that.
func TestATakeSaysAnEmptyReadyQueue(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:4")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	take := ta.ok("take --as m1 --limit 3")
	assert.Contains(t, take, "\nNOTE m1 took 2 of the 3 asked: its ready queue is empty\n")
	ta.ok("add --stream s1 --count 1")
	ta.ok("tick")
	assert.NotContains(t, ta.ok("take --as m1"), "NOTE m1", "a take of all it asked")
}

// On a twin init says a member it adds is up after the next tick, which is
// what the twin does (it beats every member at every verb) and what the help
// says.
func TestTwinInitSaysWhenAMemberIsUp(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	out := twinOK(t, file, t0, "init --readers ra,rb --members m1")
	assert.Contains(t, out, "\nNOTE a twin beats every member at every verb: each member added is up after the next nova-sprint tick\n")
	assert.Contains(t, banner(), "Every member and reader of the twin beats at every verb, so a member\nadded is up from the next tick")
	twinOK(t, file, t0, "start")
	assert.Contains(t, twinOK(t, file, t0, "tick"), "MOVED presence: m1 up\n")
}
