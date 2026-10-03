package store

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// nova-decide's layer 2 in the sprint (docs/SPEC-SPRINT.md sections 2 and 5; internal/sprint,
// decide.go) on the mem twin: the deal writes the two attempt bars on each work card, a
// failed finish is routed by its attempt decision when the class is no-result or
// nothing-to-do at or above that class's own bar and by its reason line's prefix otherwise,
// a finish carrying another take's decision is refused, and a card graded pro at or above
// the grade bar starts on pro.

// decideTake takes the work card and finishes it failed with the report and the attempt
// decision's card line, as the member does when its take was decided.
func (h *harness) decideTake(card, report, decided string) {
	h.t.Helper()
	wc := h.snap().Fleet.Card(card)
	require.NotNil(h.t, wc)
	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
		Report: report, Decided: decided, Usage: "wall=450.00s budget=1/1000", Who: wc.Row}))
}

// decidedLine is an attempt decision's card line for a take of card at attempt.
func decidedLine(class string, p float64, card string, attempt int) string {
	return decide.Decided{Value: class, P: p, Op: card + "@" + strconv.Itoa(attempt) + ".0123456789ab"}.String()
}

// The deal writes each of the sprint row's two attempt bars on the work card it cuts, by
// its own field, which the finish routes by; with no bar (the default) the card carries
// none and no decision routes it. A redeal writes the bars the row holds then, and unsets
// one the row no longer holds.
func TestTheDealWritesTheAttemptBarsOnTheWorkCard(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 2, briefOf("flash", ""))
	h.m.SetDecideBars(Bars{AttemptNoResult: "0.7", AttemptNothingToDo: "0.9"})
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	wc := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, []string{"0.7", "0.9"}, []string{wc.F(sprint.FieldDecideAttemptNoResult), wc.F(sprint.FieldDecideAttemptNothingToDo)})

	h.m.SetDecideBars(Bars{})
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
	wc2 := h.snap().Fleet.Card("s1-2.w1")
	assert.Empty(t, wc2.F(sprint.FieldDecideAttemptNoResult)+wc2.F(sprint.FieldDecideAttemptNothingToDo), "no bar: no decision routes it")

	h.m.SetDecideBars(Bars{AttemptNoResult: "0.8"})
	h.failTake("s1-1.w1", providerLine)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	wc = h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, "0.8", wc.F(sprint.FieldDecideAttemptNoResult), "the redeal writes the bar the row holds now")
	assert.Empty(t, wc.F(sprint.FieldDecideAttemptNothingToDo), "and unsets the one it no longer holds")
	h.clean("bars written")
}

// A failed finish is routed by its attempt decision for two classes alone, each at or
// above its own bar on the card: no-result ends the take (withdrawn and dealt again, never
// a failed-work judgment), nothing-to-do is failed work of the class `decided
// nothing-to-do`. Every other class (needs-pro, wrong-scope, provider-failure, done) is
// recorded and shown and never routes; a decision under its bar, or whose class's bar is
// empty, leaves the prefix rule standing; and a report the provider failed, or a staging
// refusal, is never overridden by a decision. The work card keeps the decision and whether
// it routed the finish; the primary keeps its record of the decision for the outcome.
func TestAFailedFinishGoesByItsAttemptDecisionAtItsClassBar(t *testing.T) {
	t.Parallel()
	const verdict, verdictClass = "verdict not-done; tests red in x", "verdict not-done; tests red in"
	both := Bars{AttemptNoResult: "0.7", AttemptNothingToDo: "0.7"}
	for _, tc := range []struct {
		name   string
		bars   Bars
		report string
		class  string
		p      float64
		col    string // the work card's column after the finish
		failed string // the primary's failure class; "" when no failed work
		used   bool
	}{
		{"no-result over its bar ends the take", Bars{AttemptNoResult: "0.7"}, verdict, decide.ClassNoResult, 0.93, sprint.Withdrawn, "", true},
		{"nothing-to-do over its bar is failed work of its class", Bars{AttemptNothingToDo: "0.7"}, verdict, decide.ClassNothingToDo, 0.7, sprint.DoneFailed, "decided nothing-to-do", true},
		{"no-result is read from its own bar alone", Bars{AttemptNothingToDo: "0.1"}, verdict, decide.ClassNoResult, 0.99, sprint.DoneFailed, verdictClass, false},
		{"nothing-to-do is read from its own bar alone", Bars{AttemptNoResult: "0.1"}, verdict, decide.ClassNothingToDo, 0.99, sprint.DoneFailed, verdictClass, false},
		{"needs-pro has no bar", both, verdict, decide.ClassNeedsPro, 0.99, sprint.DoneFailed, verdictClass, false},
		{"wrong-scope has no bar", both, verdict, decide.ClassWrongScope, 0.99, sprint.DoneFailed, verdictClass, false},
		{"provider-failure has no bar", both, verdict, decide.ClassProviderFailure, 0.99, sprint.DoneFailed, verdictClass, false},
		{"done never routes a failed finish", both, verdict, decide.ClassDone, 0.99, sprint.DoneFailed, verdictClass, false},
		{"under the bar the prefix stands", both, verdict, decide.ClassNoResult, 0.5, sprint.DoneFailed, verdictClass, false},
		{"with no bar the prefix stands", Bars{}, verdict, decide.ClassNoResult, 0.99, sprint.DoneFailed, verdictClass, false},
		{"a provider failure is never made failed work", both, providerLine, decide.ClassNothingToDo, 0.99, sprint.Withdrawn, "", false},
		{"a provider failure is never made no result", both, providerLine, decide.ClassNoResult, 0.99, sprint.Withdrawn, "", false},
		{"a staging refusal is the member's", both, cardhdr.EndStaging + ": no bench mirror", decide.ClassNoResult, 0.99, sprint.Withdrawn, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
			h.addReady("s1", 1, briefOf("flash", ""))
			h.m.SetDecideBars(tc.bars)
			h.must(DealStep(sprint.DealReq{}))
			line := decidedLine(tc.class, tc.p, "s1-1", 1)
			h.decideTake("s1-1.w1", tc.report, line)
			s := h.snap()
			wc, pr := s.Fleet.Card("s1-1.w1"), s.Work.Card("s1-1")
			assert.Equal(t, tc.col, wc.Col)
			assert.Equal(t, tc.failed, pr.F(sprint.FieldFailure))
			assert.Equal(t, tc.used, wc.F(sprint.FieldDecidedUsed) == "yes")
			used := map[bool]string{true: "yes", false: "no"}[tc.used]
			d, _ := decide.ParseDecided(line)
			staging := tc.report == cardhdr.EndStaging+": no bench mirror"
			if staging {
				// no take ran: no decision of one is kept
				assert.Empty(t, wc.F(sprint.FieldDecided))
				assert.Empty(t, pr.F(sprint.PrefixDecided+d.Op))
			} else {
				assert.Equal(t, line, wc.F(sprint.FieldDecided), "the work card keeps the decision")
				assert.Equal(t, fmt.Sprintf("%s p=%.3f used=%s", tc.class, tc.p, used), pr.F(sprint.PrefixDecided+d.Op))
			}
			if tc.col == sprint.Withdrawn && !staging {
				assert.NotEmpty(t, wc.F(sprint.FieldTakeEnded), "an ended take")
				assert.Zero(t, h.notesOf(sprint.NWorkFailed), "never a failed-work judgment")
			}
			if tc.class == decide.ClassNoResult && tc.used {
				assert.Contains(t, wc.F(sprint.FieldProviderTake+"1"), cardhdr.EndNoResult+": verdict not-done", "the take's record says no result, so the route's rest counts it")
			}
			if tc.report == providerLine {
				assert.Contains(t, wc.F(sprint.FieldProviderTake+"1"), "stream error", "native's own line stands in the take's record")
			}
			h.clean(tc.name)
		})
	}
	// a decision is one take's: a finish naming two cards with one is refused
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 2, briefOf("flash", ""))
	h.m.SetDecideBars(both)
	h.must(DealStep(sprint.DealReq{}))
	gens := map[string]int{}
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		wc := h.snap().Fleet.Card(id)
		gens[id] = wc.Int("gen")
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: wc.Int("gen")}, Who: wc.Row}))
	}
	as := h.snap().Fleet.Card("s1-1.w1").Row + "," + h.snap().Fleet.Card("s1-2.w1").Row
	res := h.run(FinishStep(sprint.FinishReq{As: as, Sel: sprint.Sel{IDs: []string{"s1-1.w1", "s1-2.w1"}}, Gens: gens, Failed: true, Report: "r", Decided: decidedLine(decide.ClassNoResult, 0.9, "s1-1", 1), Who: as}))
	require.Len(t, res.Refused, 2)
	assert.Contains(t, res.Refused[0].Why, "names one card")
}

// A finish is routed only by a decision of its own take: one whose op names another card,
// or another attempt of the same card, is refused in one line naming both, nothing moved and
// nothing kept, though its class is over its bar. The take's own decision then finishes it.
func TestAFinishCarryingAnotherTakesDecisionIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, card, names string }{
		{"another card's", "zz-9", "zz-9@7"},
		{"another attempt's", "s1-1", "s1-1@2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
			h.addReady("s1", 1, briefOf("flash", ""))
			h.m.SetDecideBars(Bars{AttemptNoResult: "0.7"})
			h.must(DealStep(sprint.DealReq{}))
			wc := h.snap().Fleet.Card("s1-1.w1")
			gens := map[string]int{wc.ID: wc.Int("gen")}
			h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
			attempt := map[string]int{"zz-9": 7, "s1-1": 2}[tc.card]
			other := decidedLine(decide.ClassNoResult, 0.99, tc.card, attempt)
			res := h.run(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
				Report: "verdict not-done; tests red in x", Decided: other, Who: wc.Row}))
			require.Len(t, res.Refused, 1)
			why := res.Refused[0].Why
			assert.Contains(t, why, "names "+tc.names+" ", why)
			assert.Contains(t, why, "not this take's s1-1@1 (s1-1.w1)", why)
			assert.NotContains(t, why, "\n")
			s := h.snap()
			assert.Equal(t, sprint.Working, s.Fleet.Card("s1-1.w1").Col, "not withdrawn by another take's no-result")
			assert.Empty(t, s.Fleet.Card("s1-1.w1").F(sprint.FieldDecided))
			assert.Equal(t, sprint.Working, s.Work.Card("s1-1").Col)

			h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
				Report: "verdict not-done; tests red in x", Decided: decidedLine(decide.ClassNoResult, 0.99, "s1-1", 1), Who: wc.Row}))
			assert.Equal(t, sprint.Withdrawn, h.snap().Fleet.Card("s1-1.w1").Col, "its own decision routes it")
			h.clean(tc.name)
		})
	}
}

// Two failed attempts the decision classes nothing-to-do at its bar are the second
// identical failure, though their reason lines differ: below its ceiling the machine
// escalates the card to pro at once (flash first). Under the bar the same two lines are two
// failures, and so are two decided needs-pro, which has no bar.
func TestTwoDecidedNothingToDoFailuresEscalateTheCard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		class     string
		p         float64
		escalated bool
	}{{decide.ClassNothingToDo, 0.9, true}, {decide.ClassNothingToDo, 0.6, false}, {decide.ClassNeedsPro, 0.99, false}} {
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.m.SetDecideBars(Bars{AttemptNoResult: "0.7", AttemptNothingToDo: "0.7"})
		h.startMachine()
		h.machine()
		h.decideTake("s1-1.w1", "verdict not-done; tests red in internal/a", decidedLine(tc.class, tc.p, "s1-1", 1))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Who: "tester"}))
		h.machine()
		h.decideTake("s1-1.w2", "push refused: the result's head is not a commit; r", decidedLine(tc.class, tc.p, "s1-1", 2))
		pr := h.snap().Work.Card("s1-1")
		if tc.escalated {
			assert.Equal(t, sprint.Ready, pr.Col, "escalated: back to ready for its next attempt")
			assert.Equal(t, cardhdr.RoutePro, pr.F(sprint.FieldTierNow))
			assert.Contains(t, pr.F("why"), "failed the same way (decided nothing-to-do)")
		} else {
			assert.Equal(t, sprint.Review, pr.Col, "two different failures: failed work for the coordinator")
			assert.Equal(t, cardhdr.RouteFlash, pr.F(sprint.FieldTierNow))
		}
		h.clean("decided twice")
	}
}

// A card graded pro at or above the sprint row's grade bar starts on pro instead of flash,
// when its ceiling is pro; with no bar the grade is a hint and the card starts on flash; a
// grade under the bar, a grade of flash, and a card whose ceiling is flash start on flash.
func TestAGradeOfProAtTheBarStartsTheCardOnPro(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, bar, ceiling, grade string
		p                         float64
		tier                      string
	}{
		{"pro over the bar", "0.7", "pro", decide.GradePro, 0.81, cardhdr.RoutePro},
		{"no bar: a hint", "", "pro", decide.GradePro, 0.99, cardhdr.RouteFlash},
		{"pro under the bar", "0.7", "pro", decide.GradePro, 0.6, cardhdr.RouteFlash},
		{"flash over the bar", "0.7", "pro", decide.GradeFlash, 0.9, cardhdr.RouteFlash},
		{"never above the ceiling", "0.7", "flash", decide.GradePro, 0.95, cardhdr.RouteFlash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
			h.addReady("s1", 1, briefOf(tc.ceiling, ""))
			h.m.SetDecideBars(Bars{Grade: tc.bar})
			g := decide.Decided{Value: tc.grade, P: tc.p, Op: "s1-1@grade.0123456789ab"}
			res := h.must(GradeStep(sprint.GradeReq{Grades: map[string]decide.Decided{"s1-1": g}, Who: sprint.MachineActor}))
			require.Len(t, res.Moved, 1)
			assert.Equal(t, g.String(), h.snap().Work.Card("s1-1").F(sprint.FieldGrade))
			h.must(DealStep(sprint.DealReq{}))
			wc := h.snap().Fleet.Card("s1-1.w1")
			assert.Equal(t, tc.tier, wc.F(sprint.FieldTier))
			assert.Equal(t, tc.tier, h.snap().Work.Card("s1-1").F(sprint.FieldTierNow))
			h.clean(tc.name)
		})
	}
}

// The grade is written only on a card still ungraded and never dealt: a card dealt
// meanwhile and a card graded already are passed over, saying nothing. Replacing a brief
// clears its grade, which was of the brief replaced.
func TestAGradeIsWrittenOnlyOnAnUngradedCardNeverDealt(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 3, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	g := func(p float64) decide.Decided {
		return decide.Decided{Value: decide.GradeFlash, P: p, Op: "x@grade.0123456789ab"}
	}
	h.must(GradeStep(sprint.GradeReq{Grades: map[string]decide.Decided{"s1-2": g(0.8)}}))
	res := h.must(GradeStep(sprint.GradeReq{Grades: map[string]decide.Decided{"s1-1": g(0.7), "s1-2": g(0.6), "s1-3": g(0.5)}}))
	assert.Len(t, res.Moved, 1, "s1-3 alone: s1-1 was dealt, s1-2 graded")
	s := h.snap()
	assert.Empty(t, s.Work.Card("s1-1").F(sprint.FieldGrade))
	assert.Equal(t, g(0.8).String(), s.Work.Card("s1-2").F(sprint.FieldGrade))
	assert.Equal(t, g(0.5).String(), s.Work.Card("s1-3").F(sprint.FieldGrade))
	h.must(BriefStep(sprint.BriefReq{ID: "s1-3", Brief: briefOf("flash", "PATHS: other")}))
	assert.Empty(t, h.snap().Work.Card("s1-3").F(sprint.FieldGrade), "a brief replaced is graded again")
	h.clean("grades")
}
