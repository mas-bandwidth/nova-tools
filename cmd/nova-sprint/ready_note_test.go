package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The note of cards the machine accepted names land, the step that merges and
// pushes them, never merge, which records a landing without touching git; and
// says a run started with --land lands them itself.
func TestTheReadyToMergeNoteNamesLand(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 5")
	ta.ok("finish --as m1 s1-1.w1@1 --head 0123abc")
	ta.ok("tick")
	ta.ok("read --as reader-a --ok --limit 10")
	ta.ok("read --as reader-b --ok --limit 10")
	ta.ok("tick")
	out := ta.ok("inbox")
	assert.Contains(t, out, "1 accepted and queued to merge: s1-1; run: nova-sprint land --stream s1 (a nova-sprint run started with --land lands them itself)")
	assert.NotContains(t, out, "run: nova-sprint merge")
	ta.clean()
}
