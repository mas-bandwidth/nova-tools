package update

import (
	"strings"
)

// WorkflowRunOutcome represents the status and conclusion of a GitHub Actions / CI workflow run.
type WorkflowRunOutcome struct {
	Status     string `json:"status"`     // e.g. "completed", "in_progress", "queued"
	Conclusion string `json:"conclusion"` // e.g. "success", "failure", "cancelled", "timed_out", "skipped", "neutral"
}

// RevertDecision describes whether a workflow run outcome requires a revert under revert-on-red.
type RevertDecision struct {
	RequiresRevert bool
	IsRed          bool
	Reason         string
}

// IsCancelledWorkflow reports whether a status or conclusion indicates a cancelled run.
// Both standard "cancelled" and alternative "canceled" spellings are accepted.
func IsCancelledWorkflow(statusOrConclusion string) bool {
	switch strings.ToLower(strings.TrimSpace(statusOrConclusion)) {
	case "cancelled", "canceled":
		return true
	default:
		return false
	}
}

// IsRedWorkflowRun reports whether a workflow run conclusion is considered red (failure requiring attention).
// Under revert-on-red, "cancelled" (such as the cold-cache cancellation of 1.0.0's first main run)
// is treated as red alongside explicit failures and timeouts.
// "success", "skipped", and "neutral" conclusions are green.
func IsRedWorkflowRun(conclusion string) bool {
	switch strings.ToLower(strings.TrimSpace(conclusion)) {
	case "failure", "cancelled", "canceled", "timed_out", "startup_failure", "action_required":
		return true
	default:
		return false
	}
}

// MapRunOutcome maps a GitHub Actions workflow run status and conclusion to a standardized
// outcome classification ("green", "red", or "pending").
// Cancelled runs are explicitly mapped to "red".
func MapRunOutcome(status, conclusion string) string {
	if IsRedWorkflowRun(conclusion) || IsCancelledWorkflow(status) {
		return "red"
	}
	switch strings.ToLower(strings.TrimSpace(conclusion)) {
	case "success", "skipped", "neutral":
		return "green"
	}
	st := strings.ToLower(strings.TrimSpace(status))
	if st != "completed" && st != "" {
		return "pending"
	}
	if conclusion == "" {
		return "pending"
	}
	return "red"
}

// CheckRevertOnRed evaluates whether a completed workflow run outcome requires a revert
// when revert-on-red is enabled. If revert-on-red is disabled, it never requires a revert.
// When enabled, a completed workflow run with a red conclusion (including cancelled push runs)
// requires a revert.
func CheckRevertOnRed(outcome WorkflowRunOutcome, revertOnRedEnabled bool) RevertDecision {
	isRed := IsRedWorkflowRun(outcome.Conclusion) || IsCancelledWorkflow(outcome.Status) || IsCancelledWorkflow(outcome.Conclusion)
	if !revertOnRedEnabled {
		return RevertDecision{
			RequiresRevert: false,
			IsRed:          isRed,
			Reason:         "revert-on-red disabled",
		}
	}
	if !strings.EqualFold(strings.TrimSpace(outcome.Status), "completed") {
		return RevertDecision{
			RequiresRevert: false,
			IsRed:          false,
			Reason:         "workflow run not completed",
		}
	}
	if isRed {
		concl := strings.ToLower(strings.TrimSpace(outcome.Conclusion))
		if concl == "" {
			concl = strings.ToLower(strings.TrimSpace(outcome.Status))
		}
		return RevertDecision{
			RequiresRevert: true,
			IsRed:          true,
			Reason:         "workflow run concluded " + concl,
		}
	}
	return RevertDecision{
		RequiresRevert: false,
		IsRed:          false,
		Reason:         "workflow run concluded green",
	}
}

// ShouldRevert reports whether a completed workflow run with the given conclusion
// requires a revert when revert-on-red is enabled.
func ShouldRevert(conclusion string, revertOnRedEnabled bool) bool {
	return revertOnRedEnabled && IsRedWorkflowRun(conclusion)
}
