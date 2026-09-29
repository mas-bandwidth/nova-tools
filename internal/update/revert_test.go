package update

import (
	"testing"
)

func TestCancelledTreatedAsRed(t *testing.T) {
	t.Parallel()

	// Direct conclusion checks
	redConclusions := []string{
		"failure",
		"cancelled",
		"canceled",
		"timed_out",
		"startup_failure",
		"action_required",
		"CANCELLED",
		"Cancelled",
	}
	for _, c := range redConclusions {
		if !IsRedWorkflowRun(c) {
			t.Errorf("IsRedWorkflowRun(%q) = false, want true (treated as red)", c)
		}
	}

	greenConclusions := []string{
		"success",
		"skipped",
		"neutral",
		"SUCCESS",
	}
	for _, c := range greenConclusions {
		if IsRedWorkflowRun(c) {
			t.Errorf("IsRedWorkflowRun(%q) = true, want false (green)", c)
		}
	}
}

func TestMapRunOutcome(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status     string
		conclusion string
		want       string
	}{
		{"completed", "success", "green"},
		{"completed", "skipped", "green"},
		{"completed", "neutral", "green"},
		{"completed", "failure", "red"},
		{"completed", "cancelled", "red"},
		{"completed", "canceled", "red"},
		{"completed", "timed_out", "red"},
		{"completed", "startup_failure", "red"},
		{"in_progress", "", "pending"},
		{"queued", "", "pending"},
		{"in_progress", "cancelled", "red"},
	}

	for _, tc := range cases {
		got := MapRunOutcome(tc.status, tc.conclusion)
		if got != tc.want {
			t.Errorf("MapRunOutcome(%q, %q) = %q, want %q", tc.status, tc.conclusion, got, tc.want)
		}
	}
}

func TestCheckRevertOnRed(t *testing.T) {
	t.Parallel()

	// When revert-on-red is enabled, a completed cancelled run requires a revert.
	cancelledOutcome := WorkflowRunOutcome{
		Status:     "completed",
		Conclusion: "cancelled",
	}
	decision := CheckRevertOnRed(cancelledOutcome, true)
	if !decision.RequiresRevert {
		t.Errorf("CheckRevertOnRed(cancelled, enabled=true).RequiresRevert = false, want true")
	}
	if !decision.IsRed {
		t.Errorf("CheckRevertOnRed(cancelled, enabled=true).IsRed = false, want true")
	}

	// American spelling "canceled" must also be treated as red and require revert.
	canceledOutcome := WorkflowRunOutcome{
		Status:     "completed",
		Conclusion: "canceled",
	}
	decision = CheckRevertOnRed(canceledOutcome, true)
	if !decision.RequiresRevert || !decision.IsRed {
		t.Errorf("CheckRevertOnRed(canceled, enabled=true) = %+v, want RequiresRevert=true, IsRed=true", decision)
	}

	// Success run does not require revert.
	successOutcome := WorkflowRunOutcome{
		Status:     "completed",
		Conclusion: "success",
	}
	decision = CheckRevertOnRed(successOutcome, true)
	if decision.RequiresRevert {
		t.Errorf("CheckRevertOnRed(success, enabled=true).RequiresRevert = true, want false")
	}
	if decision.IsRed {
		t.Errorf("CheckRevertOnRed(success, enabled=true).IsRed = true, want false")
	}

	// In-progress run does not require revert.
	inProgressOutcome := WorkflowRunOutcome{
		Status:     "in_progress",
		Conclusion: "",
	}
	decision = CheckRevertOnRed(inProgressOutcome, true)
	if decision.RequiresRevert {
		t.Errorf("CheckRevertOnRed(in_progress, enabled=true).RequiresRevert = true, want false")
	}

	// When revert-on-red is disabled, even a cancelled run does not require revert.
	decision = CheckRevertOnRed(cancelledOutcome, false)
	if decision.RequiresRevert {
		t.Errorf("CheckRevertOnRed(cancelled, enabled=false).RequiresRevert = true, want false")
	}
	if !decision.IsRed {
		t.Errorf("CheckRevertOnRed(cancelled, enabled=false).IsRed = false, want true (still red even when revert disabled)")
	}
}

func TestColdCacheCancellationRegression(t *testing.T) {
	t.Parallel()

	// Regression test for 1.0.0's first main run:
	// A push run on main timed out on cold cache and concluded "cancelled".
	// Revert-on-red must treat this cancelled push run as red and require revert.
	run := WorkflowRunOutcome{
		Status:     "completed",
		Conclusion: "cancelled",
	}

	if !ShouldRevert(run.Conclusion, true) {
		t.Fatalf("cold-cache cancellation of push run concluded %q did not trigger revert", run.Conclusion)
	}

	d := CheckRevertOnRed(run, true)
	if !d.RequiresRevert || !d.IsRed {
		t.Fatalf("cold-cache cancellation: got %+v, want RequiresRevert=true and IsRed=true", d)
	}
}
