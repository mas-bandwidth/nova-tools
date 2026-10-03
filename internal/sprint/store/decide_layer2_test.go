package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// nova-decide's layer 2 in the sprint (docs/SPEC-SPRINT.md sections 2 and 5; internal/sprint,
// decide.go) on the mem twin: the deal writes the attempt bar on each work card, a failed
// finish is routed by its attempt decision at or above the bar and by its reason line's
// prefix below it, and a card graded pro at or above the grade bar starts on pro.

// decideTake takes the work card and finishes it failed with the report and the attempt
// decision's card line, as the member does when its take was decided.
func (h *harness) decideTake(card, report, decided string) {
	h.t.Helper()
	wc := h.snap().Fleet.Card(card)
	require.NotNil(h.t, wc)
	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
		Report: report, Decided: decided, Who: wc.Row}))
}

// decidedLine is an attempt decision's card line for card at attempt.
func decidedLine(class string, p float64, card string) string {
	return decide.Decided{Value: class, P: p, Op: card + "@1.0123456789ab"}.String()
}

// The deal writes the sprint row's attempt bar on the work card it cuts, and its packet
// hands it to the member, who asks the attempt decision when it is set; with no bar the card
// carries none. A redeal writes the bar the row holds then.
func TestTheDealWritesTheAttemptBarAndThePacketCarriesIt(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 2, briefOf("flash", ""))
	h.m.SetDecideLayer2("0.7", "")
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	wc := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, "0.7", wc.F(sprint.FieldDecideAttempt))
	p := sprint.PacketOf("t-", 1, wc, h.snap().Work.Card("s1-1"), nil, nil)
	assert.Equal(t, "0.7", p.DecideAttempt)

	h.m.SetDecideLayer2("", "")
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
	assert.Empty(t, h.snap().Fleet.Card("s1-2.w1").F(sprint.FieldDecideAttempt), "no bar: no attempt decision")

	h.m.SetDecideLayer2("0.8", "")
	h.failTake("s1-1.w1", providerLine)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	assert.Equal(t, "0.8", h.snap().Fleet.Card("s1-1.w1").F(sprint.FieldDecideAttempt), "the redeal writes the bar the row holds now")
	h.clean("bars written")
}

// A failed finish whose attempt decision is at or above its card's bar is routed by the
// class: provider-failure and no-result end the take (withdrawn and dealt again, never a
// failed-work judgment), nothing-to-do, wrong-scope and needs-pro are failed work of that
// class. Under the bar, with no bar, for done, and for a staging refusal the reason line's
// prefix routes it as before. The work card keeps the decision and whether it routed the
// finish; the primary keeps its record of the decision for the outcome.
func TestAFailedFinishGoesByItsAttemptDecisionAtTheBar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, bar, report, class string
		p                        float64
		col                      string // the work card's column after the finish
		failed                   string // the primary's failure class; "" when no failed work
		used                     bool
	}{
		{"no-result over the bar ends the take", "0.7", "verdict not-done; tests red in x", decide.ClassNoResult, 0.93, sprint.Withdrawn, "", true},
		{"provider-failure over the bar ends the take", "0.7", "budget: no RESULT.md shape; r", decide.ClassProviderFailure, 0.9, sprint.Withdrawn, "", true},
		{"needs-pro over the bar is failed work of its class", "0.7", providerLine, decide.ClassNeedsPro, 0.9, sprint.DoneFailed, "decided needs-pro", true},
		{"wrong-scope at the bar", "0.7", "verdict not-done; r", decide.ClassWrongScope, 0.7, sprint.DoneFailed, "decided wrong-scope", true},
		{"under the bar the prefix stands", "0.7", providerLine, decide.ClassNeedsPro, 0.5, sprint.Withdrawn, "", false},
		{"with no bar the prefix stands", "", "verdict not-done; tests red in x", decide.ClassNoResult, 0.99, sprint.DoneFailed, "verdict not-done; tests red in", false},
		{"done never routes a failed finish", "0.7", "verdict not-done; tests red in x", decide.ClassDone, 0.95, sprint.DoneFailed, "verdict not-done; tests red in", false},
		{"a staging refusal is the member's", "0.7", cardhdr.EndStaging + ": no bench mirror", decide.ClassNoResult, 0.99, sprint.Withdrawn, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
			h.addReady("s1", 1, briefOf("flash", ""))
			h.m.SetDecideLayer2(tc.bar, "")
			h.must(DealStep(sprint.DealReq{}))
			line := decidedLine(tc.class, tc.p, "s1-1")
			h.decideTake("s1-1.w1", tc.report, line)
			s := h.snap()
			wc, pr := s.Fleet.Card("s1-1.w1"), s.Work.Card("s1-1")
			assert.Equal(t, tc.col, wc.Col)
			assert.Equal(t, tc.failed, pr.F(sprint.FieldFailure))
			assert.Equal(t, tc.used, wc.F(sprint.FieldDecidedUsed) == "yes")
			used := map[bool]string{true: "yes", false: "no"}[tc.used]
			d, _ := decide.ParseDecided(line)
			if staging := tc.report == cardhdr.EndStaging+": no bench mirror"; staging {
				// no take ran: no decision of one is kept
				assert.Empty(t, wc.F(sprint.FieldDecided))
				assert.Empty(t, pr.F(sprint.PrefixDecided+d.Op))
			} else {
				assert.Equal(t, line, wc.F(sprint.FieldDecided), "the work card keeps the decision")
				assert.Equal(t, fmt.Sprintf("%s p=%.3f used=%s", tc.class, tc.p, used), pr.F(sprint.PrefixDecided+d.Op))
			}
			if tc.col == sprint.Withdrawn && tc.report != cardhdr.EndStaging+": no bench mirror" {
				assert.NotEmpty(t, wc.F(sprint.FieldTakeEnded), "an ended take")
				assert.Zero(t, h.notesOf(sprint.NWorkFailed), "never a failed-work judgment")
			}
			if tc.class == decide.ClassNoResult && tc.used {
				assert.Contains(t, wc.F(sprint.FieldProviderTake+"1"), cardhdr.EndNoResult+": verdict not-done", "the take's record says no result, so the route's rest counts it")
			}
			h.clean(tc.name)
		})
	}
	// a decision is one take's: a finish naming two cards with one is refused
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 2, briefOf("flash", ""))
	h.m.SetDecideLayer2("0.7", "")
	h.must(DealStep(sprint.DealReq{}))
	gens := map[string]int{}
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		wc := h.snap().Fleet.Card(id)
		gens[id] = wc.Int("gen")
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: wc.Int("gen")}, Who: wc.Row}))
	}
	as := h.snap().Fleet.Card("s1-1.w1").Row + "," + h.snap().Fleet.Card("s1-2.w1").Row
	res := h.run(FinishStep(sprint.FinishReq{As: as, Sel: sprint.Sel{IDs: []string{"s1-1.w1", "s1-2.w1"}}, Gens: gens, Failed: true, Report: "r", Decided: decidedLine(decide.ClassNeedsPro, 0.9, "s1-1"), Who: as}))
	require.Len(t, res.Refused, 2)
	assert.Contains(t, res.Refused[0].Why, "names one card")
}

// Two failed attempts the decision classes needs-pro at the bar are the second identical
// failure, though their reason lines differ: below its ceiling the machine escalates the
// card to pro at once (flash first). Under the bar the same two lines are two failures.
// The finishes report no usage: a failed finish reads the routes all the same, so the
// escalation never waits on a usage line (store.FinishStep).
func TestTwoDecidedNeedsProFailuresEscalateTheCard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		p         float64
		escalated bool
	}{{0.9, true}, {0.6, false}} {
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.m.SetDecideLayer2("0.7", "")
		h.startMachine()
		h.machine()
		h.decideTake("s1-1.w1", "verdict not-done; tests red in internal/a", decidedLine(decide.ClassNeedsPro, tc.p, "s1-1"))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Who: "tester"}))
		h.machine()
		h.decideTake("s1-1.w2", "push refused: the result's head is not a commit; r", decidedLine(decide.ClassNeedsPro, tc.p, "s1-1"))
		pr := h.snap().Work.Card("s1-1")
		if tc.escalated {
			assert.Equal(t, sprint.Ready, pr.Col, "escalated: back to ready for its next attempt")
			assert.Equal(t, cardhdr.RoutePro, pr.F(sprint.FieldTierNow))
			assert.Contains(t, pr.F("why"), "failed the same way (decided needs-pro)")
		} else {
			assert.Equal(t, sprint.Review, pr.Col, "two different failures: failed work for the coordinator")
			assert.Equal(t, cardhdr.RouteFlash, pr.F(sprint.FieldTierNow))
		}
		h.clean("needs-pro twice")
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
			h.m.SetDecideLayer2("", tc.bar)
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
