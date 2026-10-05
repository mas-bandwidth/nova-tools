package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The attempt cap (docs/SPEC-SPRINT.md, "The brief is wrong, not the worker"; brief_bound.go
// AttemptsCap; the owner, 2026-10-04). The night before, only a repeated finding bounded a
// card: one that failed differently each time was reworked 262 times. After the cap's attempts
// on one brief (default 4; init --attempts, set --attempts, stream set --attempts) the card is
// not dealt again: one judgment "brief defect after N attempts, $X spent" lists every attempt's
// finding, with the decisions brief and drop; rework refuses it with the same line.

// brokenDifferently runs the primary's live attempt to a broken read whose finding names the
// attempt, so no two attempts fail the same way.
func (h *harness) brokenDifferently(id string) {
	h.t.Helper()
	a := h.snap().Work.Card(id).F("attempt")
	h.attemptFoundBroken(id, "internal/f"+a+".go:"+a+": wrong in attempt "+a)
}

func TestTheAttemptCapIsOneJudgmentWithEveryFindingAndTheSpend(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	rework := func() []sprint.Refusal {
		return h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"})).Refused
	}
	for a := 1; a < sprint.AttemptsDefault; a++ {
		// each attempt's work costs a dollar, reported by the member (cost.go)
		wc := h.snap().Fleet.Card(h.snap().Work.Card("s1-1").F("work"))
		g := map[string]int{wc.ID: wc.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
		h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g, Head: "h" + wc.F("attempt"), Report: "r", Usage: "input=1 actual_usd=1 actual_by=harness"}))
		h.readBrokenAt("s1-1", "internal/f"+wc.F("attempt")+".go:1: wrong in attempt "+wc.F("attempt"))
		require.Empty(t, rework(), "attempt %d of %d: reworked", a, sprint.AttemptsDefault)
		assert.Len(t, h.openOf(sprint.NBriefWrong), 0, "attempt %d: under the cap", a)
	}
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, "attempt 1: internal/f1.go:1: wrong in attempt 1\nattempt 2: internal/f2.go:1: wrong in attempt 2\nattempt 3: internal/f3.go:1: wrong in attempt 3", pr.F(sprint.FieldFindings), "the rework keeps every attempt's finding")
	// the fourth attempt: the cap, whatever it finds
	h.brokenDifferently("s1-1")
	open := h.openOf(sprint.NBriefWrong)
	require.Len(t, open, 1, "one judgment: the brief is wrong")
	n := open[0].Note
	assert.Equal(t, "s1-1: brief defect after 4 attempts, $3.00 spent; the brief is wrong, not the worker; findings: attempt 1: internal/f1.go:1: wrong in attempt 1; attempt 2: internal/f2.go:1: wrong in attempt 2; attempt 3: internal/f3.go:1: wrong in attempt 3; attempt 4: internal/f4.go:4: wrong in attempt 4; attempt 4 found: internal/f4.go:4: wrong in attempt 4", n.What)
	assert.Equal(t, []string{"brief", "drop"}, n.Decisions)
	assert.Empty(t, h.openOf(sprint.NReadBroken), "the cap's judgment, not the reader's")
	refused := rework()
	require.Len(t, refused, 1, "rework refuses a card at the cap")
	assert.True(t, strings.HasPrefix(refused[0].Why, "s1-1: brief defect after 4 attempts, $3.00 spent; the brief is wrong, not the worker; findings: attempt 1:"), refused[0].Why)
	assert.True(t, strings.HasSuffix(refused[0].Why, "run: nova-sprint brief s1-1 --brief-file <path> (a waiting card) or drop s1-1 and add it again with the brief corrected"), refused[0].Why)
	assert.Equal(t, sprint.Review, h.snap().Work.Card("s1-1").Col, "not dealt again")
	for _, c := range h.commandsOf(sprint.NBriefWrong) {
		assert.Contains(t, []string{"brief", "drop"}, c.Decision)
	}
	h.clean("at the attempt cap")
}

func TestFailedWorkReachesTheAttemptCapToo(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(SetStep(sprint.SetReq{Attempts: "2", Who: h.st.Actor}))
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", true, "")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	assert.Equal(t, "attempt 1: r", h.snap().Work.Card("s1-1").F(sprint.FieldFindings), "the failed report is the attempt's finding")
	h.finishAttempt("s1-1", true, "")
	open := h.openOf(sprint.NBriefWrong)
	require.Len(t, open, 1, "the second failure is the cap's judgment, with the cap at 2")
	assert.Contains(t, open[0].Note.What, "s1-1: brief defect after 2 attempts, nothing priced; the brief is wrong, not the worker; findings: attempt 1: r; attempt 2: r")
	assert.Equal(t, []string{"brief", "drop"}, open[0].Note.Decisions)
	assert.Empty(t, h.openOf(sprint.NWorkFailed))
	h.clean("failed work at the cap")
}

func TestTheAttemptCapIsASettingOfTheSprintAndTheStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Brief: proBrief, Stream: "s2", Count: 1}))
	assert.Equal(t, sprint.AttemptsDefault, h.snap().AttemptsCap("s1"), "the default")
	res := h.must(SetStep(sprint.SetReq{Attempts: "7", Who: h.st.Actor}))
	assert.Equal(t, "sprint attempts 7", res.Moved[0])
	assert.Equal(t, 7, h.snap().AttemptsCap("s1"))
	res = h.must(SetStep(sprint.SetReq{Streams: []string{"s2"}, Attempts: "3", Who: h.st.Actor}))
	assert.Equal(t, "stream s2 attempts 3", res.Moved[0])
	assert.Equal(t, 3, h.snap().AttemptsCap("s2"), "the stream's over the sprint's")
	assert.Equal(t, 7, h.snap().AttemptsCap("s1"))
	h.must(SetStep(sprint.SetReq{Streams: []string{"s2"}, Attempts: sprint.ReadTierDefault, Who: h.st.Actor}))
	assert.Equal(t, 7, h.snap().AttemptsCap("s2"), "default takes the stream's off")
	res = h.must(SetStep(sprint.SetReq{Attempts: sprint.ReadTierDefault, Who: h.st.Actor}))
	assert.Equal(t, "sprint attempts default (4 attempts on one brief)", res.Moved[0])
	assert.Equal(t, sprint.AttemptsDefault, h.snap().AttemptsCap("s2"))
	for _, bad := range []string{"0", "-1", "101", "many", "2.5"} {
		r := h.run(SetStep(sprint.SetReq{Attempts: bad, Who: h.st.Actor}))
		require.Len(t, r.Refused, 1, "attempts %q is refused", bad)
		assert.Contains(t, r.Refused[0].Why, "--attempts wants a whole number from 1 to 100, or default")
	}
	h.clean("the cap set")
}
