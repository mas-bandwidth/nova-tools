package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The brief's bound (docs/SPEC-SPRINT.md, "The brief is wrong, not the worker"; brief_bound.go).
// On 2026-10-03 two gating cards were reworked to attempts 262 and 17 with the same reader
// finding every time, by hand and by the automatic answer, because rework was offered and
// taken at each judgment. The same finding twice means the brief is wrong, not the worker:
// rework refuses, a --fix does not lift it, and a replaced brief resets the count.

// readBrokenAt has the attempt's readers read it (one on flash, reads_per_tier), the last of
// them finding it broken with the finding.
func (h *harness) readBrokenAt(id, finding string) {
	h.t.Helper()
	h.askReads()
	rc := placedReadsOf(h.snap(), id)
	require.NotEmpty(h.t, rc, "reads asked")
	for i, c := range rc {
		verdict, f := "ok", ""
		if i == len(rc)-1 {
			verdict, f = "broken", finding
		}
		h.must(ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: c.Row, Verdict: verdict, Finding: f, Sel: sprint.Sel{IDs: []string{c.ID}}}))
	}
}

// attemptFoundBroken runs the primary's live attempt to a broken read with the finding.
func (h *harness) attemptFoundBroken(id, finding string) {
	h.t.Helper()
	h.finishAttempt(id, false, "h"+h.snap().Work.Card(id).F("attempt"))
	h.readBrokenAt(id, finding)
}

func TestReworkRefusesACardWhoseLastTwoFindingsMatch(t *testing.T) {
	t.Parallel()
	rework := func(h *harness, fix string) []sprint.Refusal {
		h.t.Helper()
		return h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: fix, Who: "tester"})).Refused
	}
	const line = "s1-1 has failed the same way twice (attempts 1 and 2: files outside PATHS: internal/x.go); the brief is wrong, not the worker; " +
		"run: nova-sprint brief s1-1 --brief-file <path> (the brief corrected in place, its next attempt from its last pushed head), or nova-sprint drop s1-1 --reason '<why>'"
	t.Run("the same finding twice", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, route("flash-a", "flash"))
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		h.attemptFoundBroken("s1-1", "files outside PATHS: internal/x.go")
		require.Empty(t, rework(h, ""), "the first finding is reworked")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, "1", pr.F(sprint.FieldFindingAttempt), "the rework records whose finding the primary carries")
		h.attemptFoundBroken("s1-1", "Files  outside its PATHS: internal/y.go, internal/z.go")
		before := h.revisions()
		refused := rework(h, "")
		require.Len(t, refused, 1)
		assert.Equal(t, line, refused[0].Why)
		refused = rework(h, "keep to the PATHS")
		require.Len(t, refused, 1)
		assert.Equal(t, line, refused[0].Why, "a --fix changes the brief not at all")
		assert.Equal(t, sprint.Review, h.snap().Work.Card("s1-1").Col, "nothing moved")
		h.nothingWritten(before)
		// the brief replaced, as Brief writes it: the bound counts from here, and the rework
		// is its next attempt
		h.setPrimary("s1-1", map[string]string{"brief": briefOf("flash", "PATHS: internal/x.go, internal/y.go"), sprint.FieldBriefAttempt: "2"})
		require.Empty(t, rework(h, ""), "a changed brief lifts the bound")
		assert.Equal(t, 3, h.snap().Work.Card("s1-1").Int("attempt"))
		h.clean("reworked on the new brief")
	})
	t.Run("too many attempts on one brief", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, route("flash-a", "flash"))
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		for a := 1; a < sprint.AttemptsDefault; a++ {
			h.attemptFoundBroken("s1-1", "internal/f"+string(rune('0'+a))+".go:"+string(rune('0'+a))+": wrong")
			require.Empty(t, rework(h, ""), "attempt %d: a new finding each time is reworked", a)
		}
		h.attemptFoundBroken("s1-1", "internal/g.go:6: wrong")
		refused := rework(h, "")
		require.Len(t, refused, 1)
		assert.Equal(t, "s1-1: brief defect after 4 attempts, nothing priced; the brief is wrong, not the worker; "+
			"findings: attempt 1: internal/f1.go:1: wrong; attempt 2: internal/f2.go:2: wrong; attempt 3: internal/f3.go:3: wrong; attempt 4: internal/g.go:6: wrong; "+
			"run: nova-sprint brief s1-1 --brief-file <path> (the brief corrected in place, its next attempt from its last pushed head), or nova-sprint drop s1-1 --reason '<why>'", refused[0].Why)
	})
	t.Run("a card never dealt, and one whose findings differ, are at no bound", func(t *testing.T) {
		t.Parallel()
		_, at := sprint.AtBriefBound(&sprint.Card{ID: "x", Fields: map[string]string{"attempt": "0"}}, "f", 0)
		assert.False(t, at)
		_, at = sprint.AtBriefBound(&sprint.Card{ID: "x", Fields: map[string]string{"attempt": "2", "finding": "a: one", sprint.FieldFindingAttempt: "1"}}, "b: two", 0)
		assert.False(t, at)
		assert.True(t, sprint.SameFinding("files outside PATHS: a.go", "two files outside its PATHS"), "one class however worded")
		assert.False(t, sprint.SameFinding("", ""), "an empty finding is never the same as another")
		// near misses: one first clause, different findings; and one finding, differently spaced and cased
		assert.False(t, sprint.SameFinding("the test fails. TestA at a.go:1 wants 2", "the test fails. TestB at b.go:9 wants 3"), "two findings that share a first sentence are two findings")
		assert.False(t, sprint.SameFinding("the test fails\nTestA at a.go:1", "the test fails\nTestB at b.go:9"), "and that share a first line")
		assert.True(t, sprint.SameFinding("The test  fails.\n  TestA at a.go:1", "the test fails. testa at a.go:1"), "one finding, whitespace collapsed and case folded")
	})
}

// The judgment raised for a card at the brief's bound names the brief, not the worker: the two
// matching findings and their attempts, and its decisions are brief and drop, never rework.
func TestTheBoundJudgmentNamesTheBriefNotTheWorker(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	h.attemptFoundBroken("s1-1", "files outside PATHS: internal/x.go")
	require.Len(t, h.openOf(sprint.NReadBroken), 1, "the first finding is a broken read")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	h.attemptFoundBroken("s1-1", "files outside its PATHS: internal/y.go")
	assert.Empty(t, h.openOf(sprint.NReadBroken), "the second time it is not the worker's")
	open := h.openOf(sprint.NBriefWrong)
	require.Len(t, open, 1)
	n := open[0].Note
	assert.Equal(t, "a card has reached its bound: the brief is wrong, not the worker", n.Type)
	assert.Equal(t, "s1-1 has failed the same way twice (attempts 1 and 2: files outside PATHS: internal/x.go); the brief is wrong, not the worker; attempt 2 found: files outside its PATHS: internal/y.go", n.What)
	assert.Equal(t, []string{"brief", "drop"}, n.Decisions)
	for _, c := range h.commandsOf(sprint.NBriefWrong) {
		for _, l := range c.Lines {
			assert.NotContains(t, l, "rework", "the inbox prints no rework for it: %s", l)
		}
		assert.Contains(t, []string{"brief", "drop"}, c.Decision)
	}
	assert.True(t, strings.HasPrefix(h.commandsOf(sprint.NBriefWrong)[0].Lines[0], "nova-sprint brief s1-1 --brief-file"), h.commandsOf(sprint.NBriefWrong)[0].Lines)
	h.clean("the brief's bound judged")
}
