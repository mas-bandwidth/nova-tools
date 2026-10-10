package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rework engine (docs/SPEC-SPRINT.md section 8, answered by rule; rules_rework.go). A
// finish that is not ok opens the next attempt by rule, with the finding, and no judgment
// waits on the seat; at the brief's bound the card is parked in fix with the BRIEF line and
// one judgment for the seat. The bound is a setting (PropReworkBound, ReworkBoundDefault); the
// tests are the core's own rig (world), a fake clock, no sockets, no real time.

func TestReworkBoundIsASetting(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	assert.Equal(t, ReworkBoundDefault, w.s.ReworkBound("s1"), "six by default")
	w.s.Work.SetProp(PropReworkBound, "8")
	assert.Equal(t, 8, w.s.ReworkBound("s1"), "the sprint's setting is read")
	w.s.Work.SetProp(PropReworkBound, "1")
	assert.Equal(t, ReworkBoundDefault, w.s.ReworkBound("s1"), "a bound under two is no bound")
	w.s.Work.SetProp(PropReworkBound, "junk")
	assert.Equal(t, ReworkBoundDefault, w.s.ReworkBound("s1"), "an unreadable value is the default")
	w.s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: StateCol, Fields: map[string]string{PropReworkBound: "7"}})
	w.s.Work.SetProp(PropReworkBound, "8")
	assert.Equal(t, 7, w.s.ReworkBound("s1"), "the stream's setting is over the sprint's")
}

func TestBriefParkLineNamesTheFinding(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "BRIEF s1-1: internal/x/a.go:12 drops the error.",
		BriefParkLine("s1-1", "internal/x/a.go:12 drops the error. More words follow."))
	assert.Equal(t, "BRIEF s1-1: first line", BriefParkLine("s1-1", "first line\nsecond line"))
	assert.Equal(t, "BRIEF s1-1: the brief is wrong, not the worker", BriefParkLine("s1-1", ""))
}

func TestReworkFindingIsTheFinishFinding(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the tests went red at internal/x/a.go:3")
	assert.Equal(t, "the tests went red at internal/x/a.go:3", ReworkFinding(w.s, NWorkFailed, w.s.Work.Card("s1-1")))
}

func TestAFailedFinishIsReworkedByRuleWithoutAJudgment(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the tests went red at internal/x/a.go:3")
	a := answerOn(t, w, on(), NWorkFailed, "s1-1")
	require.Equal(t, RuleFailed, a.Rule)
	require.Equal(t, ActRework, a.Act, a.Why)
	rules(w, on())
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, 2, pr.Int("attempt"), "the next attempt is opened by rule")
	assert.Empty(t, openOf(w, NWorkFailed, "s1-1"), "no judgment waits on the seat")
	assert.Contains(t, pr.F(FieldFix), "the tests went red", "the finding rides the next attempt")
	assert.Len(t, logged(w, RuleFailed), 1, "the answer is on the card's timeline")
}

func TestANonOKFinishAtItsBoundIsParkedInFix(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the tests went red at internal/x/a.go:3")
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, "failed", pr.F("result"))
	// the card reaches its rework bound: its brief's attempts are spent
	pr.Fields["attempt"] = itoa(ReworkBoundDefault)
	w.s.Work.cells, w.s.Work.byPrimary = nil, nil
	a := answerOn(t, w, on(), NWorkFailed, "s1-1")
	require.Equal(t, RuleFailed, a.Rule)
	require.Equal(t, ActPark, a.Act, a.Why)
	rules(w, on())
	pr = w.s.Work.Card("s1-1")
	assert.Equal(t, Review, pr.Col, "the card is parked, not dealt again")
	assert.True(t, strings.HasPrefix(pr.F(FieldFix), "BRIEF s1-1: "), "its fix is the BRIEF line: %q", pr.F(FieldFix))
	assert.NotEmpty(t, pr.F(FieldBriefDefect), "the card is marked a brief defect")
	assert.Empty(t, openOf(w, NWorkFailed, "s1-1"), "the finish's judgment is closed: one judgment per card")
}

func TestTheReworkBoundSettingParksACardSooner(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the tests went red")
	w.s.Work.SetProp(PropReworkBound, "2")
	pr := w.s.Work.Card("s1-1")
	pr.Fields["attempt"] = "2"
	w.s.Work.cells, w.s.Work.byPrimary = nil, nil
	a := answerOn(t, w, on(), NWorkFailed, "s1-1")
	assert.Equal(t, ActPark, a.Act, a.Why)
}

func TestAFinishUnderItsBoundIsNotParked(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the tests went red")
	a := answerOn(t, w, on(), NWorkFailed, "s1-1")
	assert.Equal(t, ActRework, a.Act, "one attempt of six is no bound: the next attempt is opened")
}

func TestABrokenReadAtTheBriefBoundIsParkedByRule(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	brokenOnce(t, w, "internal/x/a.go:12 drops the error")
	require.Len(t, openOf(w, NReadBroken, "s1-1"), 1)
	w.s.Work.SetProp(PropReworkBound, "2")
	rules(w, on())
	require.Equal(t, 2, w.s.Work.Card("s1-1").Int("attempt"), "the first finding, below the bound, is reworked")
	brokenOnce(t, w, "internal/x/b.go:3 off by one")
	a := answerOn(t, w, on(), NReadBroken, "s1-1")
	require.Equal(t, RuleReadBroken, a.Rule)
	require.Equal(t, ActPark, a.Act, a.Why)
	rules(w, on())
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, Review, pr.Col, "parked in review, not dealt again")
	assert.True(t, strings.HasPrefix(pr.F(FieldFix), "BRIEF s1-1: "), "its fix is the BRIEF line: %q", pr.F(FieldFix))
	assert.NotEmpty(t, pr.F(FieldBriefDefect), "the card is marked a brief defect")
	assert.Empty(t, openOf(w, NReadBroken, "s1-1"), "the reader's judgment is closed: one judgment per card")
}

func TestTheSameFailureOnManyCardsIsAdvancedByRule(t *testing.T) {
	t.Parallel()
	w := setup(t, RuleSameFailureCards)
	report := "verdict not-done; step 2 broken: exec: go: Permission denied"
	for i := 1; i <= RuleSameFailureCards; i++ {
		id := "s1-" + itoa(i)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Failed: true, Report: report}))
	}
	for _, a := range RuleAnswers(w.s, on()) {
		if a.Type == NWorkFailed {
			require.Equal(t, RuleFailed, a.Rule)
			require.NotEqual(t, ActLeft, a.Act, a.Why)
		}
	}
	rules(w, on())
	for i := 1; i <= RuleSameFailureCards; i++ {
		pr := w.s.Work.Card("s1-" + itoa(i))
		assert.Equal(t, 2, pr.Int("attempt"), "%s: advanced by rule, never left", pr.ID)
		assert.Empty(t, pr.F(FieldTier), "%s: on its tier", pr.ID)
		assert.Empty(t, openOf(w, NWorkFailed, pr.ID), "%s: no failed-attempt judgment waits", pr.ID)
	}
}
