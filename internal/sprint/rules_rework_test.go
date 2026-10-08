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
