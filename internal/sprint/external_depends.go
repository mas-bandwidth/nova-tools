package sprint

import (
	"fmt"
	"strconv"
	"strings"
)

// ExternalOperandForm represents one of the three external operand forms
// that can appear in a DEPENDS-ON line.
type ExternalOperandForm string

const (
	// FormPrMerged: "pr repo#n merged" - waits for a PR to merge
	FormPrMerged ExternalOperandForm = "pr_merged"
	// FormBranchContains: "branch contains sha" - waits for a branch to contain a commit
	FormBranchContains ExternalOperandForm = "branch_contains"
	// FormAfter: "after RFC3339" - waits for a timestamp to pass
	FormAfter ExternalOperandForm = "after"
)

// ParseExternalOperand checks if the given DEPENDS-ON value is an external operand
// and returns its form if it is. Returns empty string if not an external operand.
func ParseExternalOperand(value string) ExternalOperandForm {
	if value == "" {
		return ""
	}

	// pr <repo>#<n> merged
	if len(value) >= 4 && value[:3] == "pr " {
		rest := value[3:]
		if idx := findAfter(rest, " merged"); idx > 0 {
			before := rest[:idx]
			// must be repo#n: exactly one slash and one #
			slash := 0
			hash := 0
			for _, c := range before {
				if c == '/' {
					slash++
				} else if c == '#' {
					hash++
				}
			}
			if slash == 1 && hash == 1 {
				return FormPrMerged
			}
		}
	}

	// <branch> contains <sha>
	if idx := findAfter(value, " contains "); idx > 0 {
		branch := value[:idx]
		sha := value[idx+8:]
		if branch != "" && sha != "" {
			return FormBranchContains
		}
	}

	// after <RFC3339>
	if len(value) >= 6 && value[:5] == "after " {
		timestamp := value[6:]
		// Basic RFC3339 validation: must be at least 20 chars
		// Format: YYYY-MM-DDTHH:MM:SSZ or with timezone offset
		if len(timestamp) >= 20 {
			// Check for valid characters: digits, T, -, :, Z, +, -
			valid := true
			for i := 0; i < len(timestamp); i++ {
				c := timestamp[i]
				if !((c >= '0' && c <= '9') || c == 'T' || c == '-' || c == ':' || c == 'Z' || c == '+' || c == '-') {
					valid = false
					break
				}
			}
			if valid {
				return FormAfter
			}
		}
	}

	return ""
}

// findAfter returns the index of substr in s, or -1 if not found.
func findAfter(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// atoi converts a string to int, returning 0 on parse error.
func atoi(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return 0
}

// ExternalOperand holds the parsed information from an external operand.
type ExternalOperand struct {
	Form     ExternalOperandForm
	Repo     string // for pr_merged: the repo (owner/repo)
	PRNumber int    // for pr_merged: the PR number
	Branch   string // for branch_contains: the branch name
	Sha      string // for branch_contains: the commit sha
	At       string // for after: the RFC3339 timestamp
}

// ParseExternalOperandDetails parses an external operand value and returns
// its detailed information. Returns empty ExternalOperand if not valid.
func ParseExternalOperandDetails(value string) ExternalOperand {
	form := ParseExternalOperand(value)
	if form == "" {
		return ExternalOperand{}
	}

	ops := ExternalOperand{Form: form}
	switch form {
	case FormPrMerged:
		rest := value[3:] // skip "pr "
		if idx := findAfter(rest, " merged"); idx > 0 {
			before := rest[:idx]
			// Parse repo#n
			if hashIdx := findAfter(before, "#"); hashIdx > 0 {
				ops.Repo = before[:hashIdx]
				ops.PRNumber = atoi(before[hashIdx+1:])
			}
		}
	case FormBranchContains:
		if idx := findAfter(value, " contains "); idx > 0 {
			ops.Branch = value[:idx]
			ops.Sha = value[idx+8:]
		}
	case FormAfter:
		ops.At = value[6:] // skip "after "
	}

	return ops
}

// String returns a human-readable description of what the operand waits for.
func (o ExternalOperand) String() string {
	switch o.Form {
	case FormPrMerged:
		return fmt.Sprintf("pr %s#%d merged", o.Repo, o.PRNumber)
	case FormBranchContains:
		return fmt.Sprintf("%s contains %s", o.Branch, o.Sha)
	case FormAfter:
		return fmt.Sprintf("after %s", o.At)
	default:
		return ""
	}
}
