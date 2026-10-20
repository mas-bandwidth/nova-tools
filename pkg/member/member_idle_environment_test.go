package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// AN IDLE REAP THAT PUBLISHED NOTHING IS THE ENVIRONMENT'S, NEVER THE CARD'S. The watch
// ends a card whose log and tree were still; a member's environment may have stopped a
// process in that tree (as `leaks --atExit` does when Developer Mode is off), so the finish
// is the no-result kind the sprint deals again, and the reason names the cause.

func TestIdleReapWithNoResultSpendsNoAttempt(t *testing.T) {
	t.Parallel()

	fin, why := Judge(Result{End: EndIdleReap, Ran: true}, Push{None: "the child committed nothing"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, EndNoResult+": no RESULT.md shape", why,
		"an idle reap with no result is the no-result kind, not the card's failed work")
}

func TestIdleReapStoppedTreeNamesTheEnvironment(t *testing.T) {
	t.Parallel()

	fin, why := Judge(Result{End: EndIdleReap, EndCause: "tree-stopped", Ran: true}, Push{None: "the child committed nothing"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, EndNoResult+": tree-stopped: no RESULT.md shape", why,
		"the no-result finish names the stopped tree that ended the run")
}

func TestIdleReapWithAShapedResultStaysTheCards(t *testing.T) {
	t.Parallel()

	fin, why := Judge(Result{End: EndIdleReap, Shaped: true, Verdict: "not-done", Report: "not done"}, Push{None: "the child committed nothing"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, EndIdleReap+": verdict not-done", why,
		"a shaped result is the card's own, whatever ended the run")
}
