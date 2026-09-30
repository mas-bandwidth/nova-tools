package main

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// viewLine is the summary line nova-table renders for the sprint's stored view
// (ntable.SummaryLine over the view and its first table), from the store.
func (ta *testApp) viewLine() string {
	ta.t.Helper()
	v, ok := ta.m.View("sprint")
	if !ok {
		ta.t.Fatal("no sprint view")
	}
	shapes, err := ta.m.Shapes(context.Background(), v.Tables[:1])
	if err != nil {
		ta.t.Fatal(err)
	}
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
	if got := ta.viewLine(); got != "STOPPED" {
		t.Fatalf("after init: %q", got)
	}
	ta.ok("add --stream s1 --count 3")
	if got := ta.viewLine(); got != "STOPPED" {
		t.Fatalf("after add, stopped: %q", got)
	}
	ta.ok("start")
	if got := ta.viewLine(); got != "0/3 0.0% -> ETA" {
		t.Fatalf("after start: %q", got)
	}
	ta.ok("stop")
	if got := ta.viewLine(); got != "STOPPED" {
		t.Fatalf("after stop: %q", got)
	}
	ta.ok("start")
	ta.ok("clear --confirm sprint")
	if got := ta.viewLine(); got != "STOPPED" {
		t.Fatalf("after clear: %q", got)
	}
	// A view stored without a state (before it had one) while STOPPED: stop,
	// which changes nothing else, writes the view's state again.
	if err := ta.m.ShowState(context.Background(), "sprint", ""); err != nil {
		t.Fatal(err)
	}
	ta.ok("stop")
	if got := ta.viewLine(); got != "STOPPED" {
		t.Fatalf("stop on a stopped machine, view without a state: %q", got)
	}
	// init on a sprint that has a machine writes the view's state from it.
	ta.ok("start")
	ta.ok("init --readers reader-a,reader-b --members m1")
	if got := ta.viewLine(); got != "0/0 0.0% -> ETA" {
		t.Fatalf("init on a running sprint: %q", got)
	}
}
