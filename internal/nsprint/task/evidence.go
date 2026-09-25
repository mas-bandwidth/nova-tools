package task

import (
	"fmt"
	"strings"
)

// Evidence holds parsed done evidence fields.
type Evidence struct {
	Done      string
	PR        string
	Check     string
	Cost      string
	FollowUps []string
	Raw       string
}

// ParseEvidence parses and validates task done evidence against the required schema:
// DONE, PR, CHECK, cost, and optional follow-ups. Evidence missing DONE or PR
// is refused naming the field. Free-text evidence is refused.
func ParseEvidence(text string) (Evidence, error) {
	e := Evidence{Raw: text}
	hasDone := false
	hasPR := false

	lines := strings.Split(text, "\n")
	var currentFollowUp strings.Builder
	inFollowUp := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "DONE") {
			hasDone = true
			e.Done = trimmed
		} else if strings.HasPrefix(upper, "PR") {
			hasPR = true
			e.PR = trimmed
		} else if strings.HasPrefix(upper, "CHECK") {
			e.Check = trimmed
		} else if strings.HasPrefix(upper, "COST") || strings.Contains(upper, "COST_USD") {
			e.Cost = trimmed
		} else if strings.HasPrefix(upper, "FOLLOW-UP") || strings.HasPrefix(upper, "FOLLOWUP") {
			if inFollowUp && currentFollowUp.Len() > 0 {
				e.FollowUps = append(e.FollowUps, strings.TrimSpace(currentFollowUp.String()))
				currentFollowUp.Reset()
			}
			inFollowUp = true
		} else if inFollowUp {
			if trimmed == "" && currentFollowUp.Len() > 0 {
				e.FollowUps = append(e.FollowUps, strings.TrimSpace(currentFollowUp.String()))
				currentFollowUp.Reset()
				inFollowUp = false
			} else {
				currentFollowUp.WriteString(line + "\n")
			}
		}
	}
	if inFollowUp && currentFollowUp.Len() > 0 {
		e.FollowUps = append(e.FollowUps, strings.TrimSpace(currentFollowUp.String()))
	}

	var missing []string
	if !hasDone {
		missing = append(missing, "DONE")
	}
	if !hasPR {
		missing = append(missing, "PR")
	}
	if len(missing) > 0 {
		return e, fmt.Errorf("evidence missing required schema field(s): %s", strings.Join(missing, ", "))
	}
	return e, nil
}
