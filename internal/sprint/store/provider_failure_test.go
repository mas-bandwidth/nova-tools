package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A provider failure is an ended take, never the card's failure (docs/SPEC-SPRINT.md,
// the work card's redeals; tla/CardContract.tla, ProviderFailure; tla/DirtyTick.tla,
// RedealsAreEndedTakes), on the mem twin: the member's failed finish whose report begins
// `provider failure` withdraws the card for the next deal, which counts the take against
// the redeal bound and leaves out the routes the card was drawn when another remains.

// providerLine is a member's report of a take the provider failed.
const providerLine = cardhdr.EndProvider + ": stream error: server_error h2 protocol error"

// failTake takes the work card and finishes it failed with the report, as the member
// does when its child ends.
func (h *harness) failTake(card, report string) *sprint.Card {
	h.t.Helper()
	wc := h.snap().Fleet.Card(card)
	require.NotNil(h.t, wc)
	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
		Report: report, Usage: "wall=450.00s budget=1/1000", Who: wc.Row}))
	return wc
}

// A provider failure withdraws the card with the take ended and puts no failed-work
// judgment in the inbox; the next deal places it again on another route, counting the
// take; the route's stats count it.
func TestAProviderFailureRedealsTheCardAndIsNeverAFailedWorkJudgment(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	w := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Ready, w.Col)
	first, member := w.F(sprint.FieldRoute), w.Row

	h.failTake("s1-1.w1", providerLine)
	w = h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Withdrawn, w.Col, "the take ended: the card is withdrawn for the deal")
	assert.NotEmpty(t, w.F(sprint.FieldTakeEnded))
	takes, _ := sprint.ProviderTakes(w)
	require.Len(t, takes, 1)
	assert.Equal(t, first, takes[0].Route, "the card records the route that failed")
	assert.Equal(t, member, takes[0].Member)
	assert.Contains(t, w.F(sprint.FieldProviderError), "server_error h2 protocol error")
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, sprint.Ready, pr.Col, "its primary is ready to be dealt again, not in review")
	assert.Equal(t, 0, pr.Int("failed"), "the card's failure count does not move")
	assert.Empty(t, h.openOf(sprint.NWorkFailed), "no failed-work judgment")
	assert.Zero(t, h.notesOf(sprint.NWorkFailed))

	h.machine()
	w = h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Ready, w.Col, "dealt again by the tick at once")
	assert.Equal(t, 1, w.Int("redeals"), "the ended take counts toward the bound")
	assert.NotEqual(t, first, w.F(sprint.FieldRoute), "another route remains: the redeal leaves the failed one out")
	assert.Empty(t, h.openOf(sprint.NWorkFailed))
	for _, x := range sprint.RouteStats([]sprint.Route{route("flash-a", "flash"), route("flash-b", "flash")}, h.snap().Fleet) {
		if x.Route.Name == first {
			assert.Equal(t, [3]int{1, 1, 1}, [3]int{x.Attempts, x.Failed, x.Provider}, "the route's provider failures count apart")
		}
	}
	lines := sprint.AttemptLines(w)
	require.Len(t, lines, 2, "the failed take keeps its own line; the new take is the card's")
	for _, want := range []string{"take=1", "route=" + first, "model=prov-" + first + "/model-" + first, "member=" + member,
		"usage=wall=450.00s budget=1/1000", "end=provider failure: stream error: server_error h2 protocol error"} {
		assert.Contains(t, lines[0], want)
	}
	assert.Contains(t, lines[1], "route="+w.F(sprint.FieldRoute), "the new take's own line")
	assert.Contains(t, lines[1], "end=in flight (ready)")
	assert.Empty(t, w.F(sprint.FieldProviderError), "dealt again: the current error is cleared, the take's record is not")
	h.clean("redealt after a provider failure")
}

// With one route the redeal runs on it again: the card is left out of nothing it could
// run on.
func TestAProviderFailureWithOneRouteIsRedealtToThatRoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	h.failTake("s1-1.w1", providerLine)
	h.machine()
	w := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Ready, w.Col)
	assert.Equal(t, "flash-a", w.F(sprint.FieldRoute), "no other route remains")
	assert.Equal(t, 1, w.Int("redeals"))
}

// Three provider failures in a row are three redeals; the fourth ends the take with
// the bound reached, and the card is retired with one judgment naming the provider and
// the last error line, never a failed-work judgment.
func TestAFourthProviderFailureRetiresTheCardWithOneJudgmentNamingTheProvider(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	for i := 1; i <= sprint.MaxRedeals; i++ {
		h.failTake("s1-1.w1", providerLine)
		h.machine()
		w := h.snap().Fleet.Card("s1-1.w1")
		require.Equal(t, sprint.Ready, w.Col, "failure %d is dealt again", i)
		require.Equal(t, i, w.Int("redeals"))
	}
	assert.Empty(t, h.openOf(sprint.NBound), "no bound judgment before the fourth")
	h.failTake("s1-1.w1", cardhdr.EndProvider+": stream error: the last one")
	h.machine()
	h.machine()
	w := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Withdrawn, w.Col, "retired: not dealt again")
	open := h.openOf(sprint.NBound)
	require.Len(t, open, 1, "one judgment")
	what := open[0].Note.What
	assert.Contains(t, what, "prov-flash-a", "it names the provider")
	assert.Contains(t, what, "stream error: the last one", "and the last error line")
	assert.Empty(t, h.openOf(sprint.NWorkFailed), "never a failed-work judgment")
	assert.Zero(t, h.notesOf(sprint.NWorkFailed))
	h.clean("retired by provider failures")
}

// A failed finish that does not begin with the provider's kind is the card's own
// failure, as before: done failed, its primary in review, a failed-work judgment open. The
// member's reasons for a refused push and a result with the shape are among them, whatever
// the run's end was.
func TestAFailedFinishWithoutTheProviderKindStaysFailedWork(t *testing.T) {
	t.Parallel()
	for _, report := range []string{"no RESULT.md shape; " + strings.ToUpper(cardhdr.EndProvider), "push refused: rejected; r", "nothing to do: done already; r", "verdict not-done; r"} {
		h := routeHarness(t, route("flash-a", "flash"))
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		h.failTake("s1-1.w1", report)
		assert.Equal(t, sprint.DoneFailed, h.snap().Fleet.Card("s1-1.w1").Col, report)
		assert.Equal(t, sprint.Review, h.snap().Work.Card("s1-1").Col, report)
		assert.Equal(t, 1, h.notesOf(sprint.NWorkFailed), report)
	}
}

// noResultLine is a member's report of a take whose child left no result at all.
const noResultLine = cardhdr.EndNoResult + ": no RESULT.md shape; quack"

// A take whose child left no result is an ended take too (the owner, 2026-10-01, on six
// such failed-work judgments in one fleet pass: "that's fine with me."): no work came
// back, so nothing is judged; the card is withdrawn and the next deal places it again on
// another route, counting the take; its record says which kind of end it was.
func TestATakeThatLeftNoResultIsRedealtAndNeverAFailedWorkJudgment(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	first := h.snap().Fleet.Card("s1-1.w1").F(sprint.FieldRoute)

	h.failTake("s1-1.w1", noResultLine)
	w := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Withdrawn, w.Col, "the take ended: the card is withdrawn for the deal")
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, sprint.Ready, pr.Col, "its primary is ready to be dealt again, not in review")
	assert.Equal(t, 0, pr.Int("failed"), "the card's failure count does not move")
	assert.Empty(t, h.openOf(sprint.NWorkFailed), "no failed-work judgment")
	assert.Zero(t, h.notesOf(sprint.NWorkFailed))

	h.machine()
	w = h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Ready, w.Col, "dealt again by the tick at once")
	assert.Equal(t, 1, w.Int("redeals"), "the ended take counts toward the bound")
	assert.NotEqual(t, first, w.F(sprint.FieldRoute), "another route remains: the redeal leaves the one that left no result out")
	lines := sprint.AttemptLines(w)
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "end=no result: no RESULT.md shape; quack", "the take's line names its own kind")
	assert.NotContains(t, lines[0], cardhdr.EndProvider, "and not the provider's")
	h.clean("redealt after a take that left no result")
}

// Rule 2 (nova-tools#5174, the owner, 2026-10-02: "Escalate on the second identical failure,
// not the third."): a second take that left no result ends the card's tries on its tier at
// once. The card is not dealt a third time; the bound's judgment names how both ended, never
// a failed-work one; a rework with a fix is its next attempt.
func TestASecondIdenticalFailureRaisesTheBoundAtOnce(t *testing.T) {
	t.Parallel()
	// flash cards at their ceiling: the bound is the judgment (below it the machine
	// escalates: flash_first_test.go)
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	h.failTake("s1-1.w1", noResultLine)
	h.machine()
	require.Equal(t, sprint.Ready, h.snap().Fleet.Card("s1-1.w1").Col, "the first is dealt again")
	assert.Empty(t, h.openOf(sprint.NBound), "no bound judgment after the first")
	h.failTake("s1-1.w1", cardhdr.EndNoResult+": no RESULT.md shape; another line")
	h.machine()
	h.machine()
	w := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Withdrawn, w.Col, "not dealt a third time")
	assert.Equal(t, 1, w.Int("redeals"), "one redeal, below MaxRedeals: the identical ends are the bound")
	open := h.openOf(sprint.NBound)
	require.Len(t, open, 1, "one judgment")
	assert.Contains(t, open[0].Note.What, "its last two takes ended the same way (no result)")
	assert.Contains(t, open[0].Note.What, "the second identical failure")
	assert.Zero(t, h.notesOf(sprint.NWorkFailed), "never a failed-work judgment")
	res := h.run(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.Len(t, res.Refused, 1, "the deal verb refuses it too")
	assert.Contains(t, res.Refused[0].Why, "its last two takes ended the same way (no result)")
	h.clean("bound at the second identical end")

	// a rework is its next attempt, and closes the judgment
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "on pro now", Who: "tester"}))
	h.machine()
	assert.Empty(t, h.openOf(sprint.NBound))
	assert.Equal(t, 2, h.snap().Work.Card("s1-1").Int("attempt"))

	// failed work: the second attempt failing the way the first did is the bound's judgment
	// in place of the failed-work one, held by the tick while the primary stays in review;
	// a different failure after it is failed work again
	h2 := routeHarness(t, route("flash-a", "flash"))
	h2.addReady("s1", 1, briefOf("flash", ""))
	h2.startMachine()
	h2.machine()
	h2.failTake("s1-1.w1", "verdict not-done; tests red in x")
	assert.Equal(t, 1, h2.notesOf(sprint.NWorkFailed), "the first is failed work")
	h2.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "make them pass", Who: "tester"}))
	h2.machine()
	h2.failTake("s1-1.w2", "verdict not-done; tests red in y")
	assert.Equal(t, 1, h2.notesOf(sprint.NWorkFailed), "the second identical one is not failed work")
	for range 2 {
		h2.machine()
		open := h2.openOf(sprint.NBound)
		require.Len(t, open, 1, "the bound's judgment, held by the tick")
		assert.Contains(t, open[0].Note.What, "attempts 1 and 2 failed the same way (verdict not-done; tests red in)")
		assert.Equal(t, "s1-1.w2", open[0].Note.Card)
	}
	assert.Empty(t, h2.openOf(sprint.NStranded), "never stranded: the bound holds it")
	pr := h2.snap().Work.Card("s1-1")
	assert.Equal(t, sprint.Review, pr.Col)
	assert.Equal(t, "2", pr.F(sprint.FieldIdenticalAt))
	h2.clean("bound at the second identical failed attempt")
	h2.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "on the next tier", Who: "tester"}))
	h2.machine()
	assert.Empty(t, h2.openOf(sprint.NBound), "the rework closed it")
	h2.failTake("s1-1.w3", "push refused: rejected; r")
	assert.Equal(t, 2, h2.notesOf(sprint.NWorkFailed), "another failure is failed work")
	h2.machine()
	assert.Empty(t, h2.openOf(sprint.NBound))
}

// A run its budget or its deadline ended with no result is still the card's to be judged:
// the member says the end first, so the report is not of the no-result kind.
func TestARunItsBudgetEndedWithNoResultStaysFailedWork(t *testing.T) {
	t.Parallel()
	for _, report := range []string{"budget: no RESULT.md shape; r", "deadline: no RESULT.md shape; r", "no RESULT.md shape; r"} {
		h := routeHarness(t, route("flash-a", "flash"))
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		h.failTake("s1-1.w1", report)
		assert.Equal(t, sprint.DoneFailed, h.snap().Fleet.Card("s1-1.w1").Col, report)
		assert.Equal(t, 1, h.notesOf(sprint.NWorkFailed), report)
	}
}
