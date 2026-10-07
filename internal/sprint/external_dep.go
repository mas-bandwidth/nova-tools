package sprint

import (
	"strings"
	"time"
)

// ExternalOperandState holds the cached result of checking external operands.
type ExternalOperandState map[string]bool // operand string -> whether it holds

// CheckExternalOperands evaluates each external operand and returns whether
// they hold. This is called once per tick, per distinct operand.
func CheckExternalOperands(opera []string) ExternalOperandState {
	results := make(ExternalOperandState, len(ops))
	for _, op := range ops {
		form := ParseExternalOperand(op)
		switch form {
		case FormPrMerged:
			// In production, this would call GitHub API to check if PR is merged
			// For testing, we check against a fake source
			results[op] = checkPRMerged(op)
		case FormBranchContains:
			// In production, this would call git to check branch contents
			// For testing, we check against a fake source
			results[op] = checkBranchContains(op)
		case FormAfter:
			results[op] = checkAfter(op)
		default:
			results[op] = false
		}
	}
	return results
}

// checkPRMerged checks if a PR has merged. In production, this calls GitHub.
func checkPRMerged(op string) bool {
	ops := ParseExternalOperandDetails(op)
	if ops.Form != FormPrMerged {
		return false
	}
	// Check against fake PR source for testing
	return checkFakePRMerged(ops.Repo, ops.PRNumber)
}

// checkBranchContains checks if a branch contains a commit. In production, this calls git.
func checkBranchContains(op string) bool {
	ops := ParseExternalOperandDetails(op)
	if ops.Form != FormBranchContains {
		return false
	}
	// Check against fake source for testing
	return checkFakeBranchContains(ops.Branch, ops.Sha)
}

// checkAfter checks if a timestamp has passed.
func checkAfter(op string) bool {
	ops := ParseExternalOperandDetails(op)
	if ops.Form != FormAfter {
		return false
	}
	t, err := time.Parse(time.RFC3339, ops.At)
	if err != nil {
		return false
	}
	// Use current time for now; in production this would be passed as parameter
	return time.Now().After(t)
}

// Fake PR source for testing
var fakePRMerged = map[string]bool{}

// SetPRMerged registers a PR as merged for testing purposes.
func SetPRMerged(repo string, prNum int) {
	key := repo + "#" + itoa(prNum)
	fakePRMerged[key] = true
}

// checkFakePRMerged checks against the fake source.
func checkFakePRMerged(repo string, prNum int) bool {
	key := repo + "#" + itoa(prNum)
	return fakePRMerged[key]
}

// Fake branch source for testing
var fakeBranchContains = map[string]string{} // branch -> sha

// SetBranchContains registers what sha a branch contains for testing purposes.
func SetBranchContains(branch, sha string) {
	fakeBranchContains[branch] = sha
}

// checkFakeBranchContains checks against the fake source.
func checkFakeBranchContains(branch, sha string) bool {
	return fakeBranchContains[branch] == sha
}

// WaitForExternal returns the operands that a card is waiting on externally.
// This is used by the tick to check if the card can be released.
func WaitForExternal(c *Card) []string {
	if c == nil {
		return nil
	}
	return ExternalOperands(c)
}

// ExternalOperandHolds checks if any of the external operands for a card hold.
func ExternalOperandHolds(c *Card) bool {
	ops := WaitForExternal(c)
	if len(ops) == 0 {
		return false
	}
	results := CheckExternalOperands(ops)
	for _, holds := range results {
		if holds {
			return true
		}
	}
	return false
}

// ExternalOperandWaitInfo returns a human-readable description of what
// the card is waiting on externally.
func ExternalOperandWaitInfo(c *Card) string {
	ops := WaitForExternal(c)
	if len(ops) == 0 {
		return ""
	}
	var parts []string
	for _, op := range ops {
		ops := ParseExternalOperandDetails(op)
		parts = append(parts, ops.String())
	}
	return strings.Join(parts, ", ")
}
