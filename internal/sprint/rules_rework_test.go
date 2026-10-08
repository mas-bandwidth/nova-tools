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
	assert.Equal(t, AttemptsDefault, w.s.ReworkBound("s1"), "the attempt cap by default")
	w.s.Work.SetProp(PropAttempts, "3")
	assert.Equal(t, 3, w.s.ReworkBound("s1"), "the attempt cap (set --attempts) is the bound while none is set")
	w.s.Work.SetProp(PropReworkBound, "8")
	assert.Equal(t, 8, w.s.ReworkBound("s1"), "the sprint's rework bound is read over the cap")
	w.s.Work.SetProp(PropReworkBound, "1")
	assert.Equal(t, 3, w.s.ReworkBound("s1"), "a bound under two is no bound: the cap")
	w.s.Work.SetProp(PropReworkBound, "junk")
	assert.Equal(t, 3, w.s.ReworkBound("s1"), "an unreadable value is no bound: the cap")
	w.s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: StateCol, Fields: map[string]string{PropReworkBound: "7"}})
	w.s.Work.SetProp(PropReworkBound, "8")
	assert.Equal(t, 7, w.s.ReworkBound("s1"), "the stream's setting is over the sprint's")
	var none *Snapshot
	assert.Equal(t, AttemptsDefault, none.ReworkBound("s1"), "no snapshot is the default")
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
	pr.Fields["attempt"] = itoa(w.s.ReworkBound("s1"))
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
	open := w.openOn("s1-1")
	require.Len(t, open, 1, "the park raised the bound's judgment, the one for the seat: %+v", open)
	assert.Equal(t, NBriefWrong, open[0].Note.Type)
	assert.Equal(t, Decisions[NBriefWrong], open[0].Note.Decisions, "brief or drop, never rework")
	assert.True(t, strings.HasPrefix(open[0].Note.What, "brief defect: BRIEF s1-1: the tests went red"), open[0].Note.What)
	assert.Equal(t, ruleWho(RuleBriefDefect), open[0].Note.Who)

	// the next tick leaves it: the bound's judgment stays the one, and nothing parks it twice
	for _, a := range RuleAnswers(w.s, on()) {
		if a.Subject == "s1-1" {
			assert.False(t, a.Answers(), "a parked card is left for a mind: %s %s", a.Act, a.Why)
		}
	}
	rules(w, on())
	assert.Len(t, w.openOn("s1-1"), 1, "still one judgment")
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
	rules(w, on())
	assert.Empty(t, openOf(w, NWorkFailed, "s1-1"), "the finish's judgment is closed")
	assert.Len(t, openOf(w, NBriefWrong, "s1-1"), 1, "the bound's judgment is raised under the attempt cap")
}

func TestTheBoundsJudgmentOpenParksAFailedFinishWhateverTheReworkBound(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.s.Work.SetProp(PropAttempts, "1")    // the finish step raises the bound's judgment at the first attempt
	w.s.Work.SetProp(PropReworkBound, "8") // a rework bound above the cap never lifts it
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	wc := w.s.Fleet.Card("s1-1.w1")
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Failed: true, Report: "the tests went red at internal/x/a.go:3"}))
	require.Len(t, openOf(w, NBriefWrong, "s1-1"), 1, "the finish step raised the bound's judgment at the cap: %+v", w.s.Open)
	a := answerOn(t, w, on(), NBriefWrong, "s1-1")
	require.Equal(t, ActPark, a.Act, a.Why)
	p, _ := TickRuleBrief(w.s, on())
	require.Len(t, p.Units, 1, "one unit per card")
	assert.Empty(t, p.Units[0].Notes, "the bound's judgment is open already: none raised")
	assert.Empty(t, p.Units[0].Closes, "the bound's judgment is kept, the one for the seat")
	require.Len(t, p.Updates, 1, "the bound's judgment is reworded")
	assert.True(t, strings.HasPrefix(p.Updates[0].What, "brief defect: s1-1: brief defect after 1 attempts"), p.Updates[0].What)
	rules(w, on())
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, 1, pr.Int("attempt"), "no next attempt is opened against the bound")
	assert.Equal(t, Review, pr.Col)
	assert.Equal(t, "BRIEF s1-1: the tests went red at internal/x/a.go:3", pr.F(FieldFix), "the BRIEF line carries the exact finding")
	open := w.openOn("s1-1")
	require.Len(t, open, 1, "one judgment per card: %+v", open)
	assert.Equal(t, NBriefWrong, open[0].Note.Type)
	assert.Len(t, w.notesOf(NBriefWrong), 1, "the park raised no second bound judgment")

	// a failed finish whose bound's judgment is open parks on it too, never a rework
	w2 := heldFor(t, "the tests went red at internal/x/a.go:3")
	w2.s.Work.SetProp(PropReworkBound, "8")
	w2.note(parkJudgment(w2.s, w2.s.Work.Card("s1-1"), "BRIEF s1-1: the tests went red at internal/x/a.go:3", "by hand"))
	b := answerOn(t, w2, on(), NWorkFailed, "s1-1")
	require.Equal(t, ActPark, b.Act, b.Why)
	assert.Contains(t, b.Why, "the bound's judgment is open")
	rules(w2, on())
	assert.Equal(t, 1, w2.s.Work.Card("s1-1").Int("attempt"), "not reworked")
	assert.Empty(t, openOf(w2, NWorkFailed, "s1-1"), "the finish's judgment is closed")
	assert.Len(t, w2.openOn("s1-1"), 1, "one judgment per card")
}

func TestAFinishUnderItsBoundIsNotParked(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the tests went red")
	a := answerOn(t, w, on(), NWorkFailed, "s1-1")
	assert.Equal(t, ActRework, a.Act, "one attempt of six is no bound: the next attempt is opened")
}
