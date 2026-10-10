package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// viewLine is the summary line nova-table renders for the sprint's stored view
// (ntable.SummaryLine over the view and its first table), from the store.
func (ta *testApp) viewLine() string {
	ta.t.Helper()
	v, ok := ta.m.View("sprint")
	require.True(ta.t, ok, "no sprint view")
	shapes, err := ta.m.Shapes(context.Background(), v.Tables[:1])
	require.NoError(ta.t, err)
	return ntable.SummaryLine(v, shapes[0])
}

// The view's summary line is STOPPED, and nothing more, while the machine is
// STOPPED (docs/SPEC-SPRINT.md section 1): after init, stop and clear; the
// progress line while it is RUNNING; and a view that lost its state shows the
// machine again at the next stop.
func TestTheViewsSummaryLineIsStoppedAloneWhileTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	require.Equal(t, "STOPPED", ta.viewLine(), "after init")
	ta.ok("add --stream s1 --count 3")
	require.Equal(t, "STOPPED", ta.viewLine(), "after add, stopped")
	ta.ok("start")
	require.Equal(t, "0/3 0.0% -> ETA", ta.viewLine(), "after start")
	ta.ok("stop --reason r --until 9999h")
	require.Equal(t, "STOPPED", ta.viewLine(), "after stop")
	ta.ok("start")
	ta.ok("clear --confirm sprint")
	require.Equal(t, "STOPPED", ta.viewLine(), "after clear")
	// A view stored without a state (before it had one) while STOPPED: stop,
	// which changes nothing else, writes the view's state again.
	require.NoError(t, ta.m.ShowState(context.Background(), "sprint", ""))
	ta.ok("stop --reason r --until 9999h")
	require.Equal(t, "STOPPED", ta.viewLine(), "stop on a stopped machine, view without a state")
	// init on a sprint that has a machine writes the view's state from it.
	ta.ok("start")
	ta.ok("init --readers reader-a,reader-b --members m1")
	require.Equal(t, "0/0 0.0% -> ETA", ta.viewLine(), "init on a running sprint")
}

// The view init stores names no rule that hides a table or a row: every table
// shows every row, empty or not (docs/SPEC-SPRINT.md, the view).
func TestTheStoredViewHoldsTheFourTablesAndHidesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	v, ok := ta.m.View("sprint")
	require.True(t, ok, "no sprint view")
	require.Len(t, v.Tables, 4, "the view holds %v, want the four tables of the view", v.Tables)
}
