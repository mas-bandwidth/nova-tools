package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redo is the one verb for a conflict (docs/SPEC-SPRINT.md section 7, redo):
// refused, writing nothing, off a conflict; on one, the card is reworked on
// the current tip and the stream resumed in one step, the card's history one
// line for it.
func TestRedoIsOneVerbAndOneHistoryLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 5")
	ta.ok("read --as reader-b --ok --limit 5")
	ta.ok("accept --stream s1")
	ta.ok("tick")
	ta.ok("merge --stream s1 --batch 2 --conflict s1-2")
	ta.ok("tick")
	ta.ok("stop") // a stopped machine applies the step at once: no queued line before the pump's
	before := ta.applies()
	for _, line := range []string{"redo s1-3", "redo s1-2 s1-3", "redo --stream s2", "redo s1-2 --stream s2"} {
		code, _, errs := ta.do(line)
		assert.NotEqual(t, 0, code, "%s: exit %d %s", line, code, errs)
		assert.Contains(t, errs, "REFUSED", "%s: exit %d %s", line, code, errs)
	}
	assert.Equal(t, before, ta.applies(), "a refused redo wrote")
	code, _, errs := ta.do("redo")
	assert.Equal(t, 2, code, "redo naming nothing: exit %d %s", code, errs)

	out := ta.ok("redo s1-2")
	assert.Contains(t, out, "s1-2 merging -> working (redo", "redo: %s", out)
	assert.Contains(t, out, "stream s1 stopped -> merging", "redo: %s", out)
	inbox := ta.ok("inbox")
	assert.Contains(t, inbox, "judgments=0", "a judgment is open after redo:\n%s", inbox)
	assert.Contains(t, inbox, "answered by redo", "the conflict judgment is not answered by redo:\n%s", inbox)
	log := ta.ok("log --card s1-2")
	n := 0
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, "s1-2 redone by") {
			n++
		}
	}
	require.Equal(t, 1, n, "the card's history has %d redo lines, want one:\n%s", n, log)
	card := ta.ok("card s1-2")
	assert.Contains(t, card, "working", "card s1-2 after redo:\n%s", card)
	ta.clean()
}
