package pulse

import (
	"os"
	"path/filepath"
	"strings"
)

// PlanFile is the plan a pro card writes as turn one (#2590).
const PlanFile = "PLAN.md"

// PlanOKLine is the approval line a friend appends to PlanFile. It counts
// only as the last non-empty line, alone on that line, so a plan that
// mentions PLAN-OK in prose is not approved.
const PlanOKLine = "PLAN-OK"

// PlanStepReady reports whether a pro card may execute: PlanFile exists,
// ends with the PLAN-OK line, and every ask (if any) is bounded multiple
// choice. A card with no asks is not blocked by the ask rule.
func PlanStepReady(jobDir string, asks []string) (bool, string) {
	planPath := filepath.Join(jobDir, PlanFile)
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false, "plan file missing"
	}
	if !planApproved(string(data)) {
		return false, "waiting for PLAN-OK"
	}
	if !boundedAsks(asks) {
		return false, "asks not bounded multiple choice (each ask must list options in parentheses like (A / B / C))"
	}
	return true, ""
}

// planApproved is true when the last non-empty line of the plan is exactly
// PlanOKLine (surrounding whitespace ignored).
func planApproved(content string) bool {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		return l == PlanOKLine
	}
	return false
}

// boundedAsks is true when every ask carries a bounded option list. No asks
// is bounded (nothing to answer).
func boundedAsks(asks []string) bool {
	for _, a := range asks {
		if !boundedAsk(a) {
			return false
		}
	}
	return true
}

// boundedAsk is true when the ask ends with a parenthesised option list of
// at least two non-empty options separated by "/", e.g. "(A / B / C)".
func boundedAsk(a string) bool {
	s := strings.TrimSpace(a)
	if !strings.HasSuffix(s, ")") {
		return false
	}
	open := strings.LastIndex(s, "(")
	if open < 0 {
		return false
	}
	inner := s[open+1 : len(s)-1]
	if strings.ContainsAny(inner, "()") {
		return false
	}
	opts := strings.Split(inner, "/")
	if len(opts) < 2 {
		return false
	}
	for _, o := range opts {
		if strings.TrimSpace(o) == "" {
			return false
		}
	}
	return true
}

// PlanModeLine is the card line that puts a pro card in plan mode: `PLAN: PLAN.md`.
// Plan mode is the card's own declaration, so pro cards cut before it fold as they did.
const PlanModeLine = "PLAN:"

// planHold is PlanStepReady's production caller (Stella's HOLD on #3165): harvest asks
// it before a finished card is pushed. A pro card that declares plan mode may not proceed
// until its job directory's PLAN.md ends with PLAN-OK and every Q: line in it is bounded
// multiple choice; held is true with PlanStepReady's reason while it may not. Flash cards
// skip plan mode (#2590), and so does a card with no PLAN: line.
func planHold(jobDir string, c CardRow) (why string, held bool) {
	if c.Model != "pro" || !cardPlansFirst(readCard(c.Card)) {
		return "", false
	}
	ready, why := PlanStepReady(jobDir, planAsks(jobDir))
	return why, !ready
}

// cardPlansFirst is true when the card carries `PLAN: PLAN.md` before its first section.
func cardPlansFirst(card string) bool {
	for _, l := range strings.Split(card, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") {
			return false
		}
		if v, ok := strings.CutPrefix(t, PlanModeLine); ok && strings.TrimSpace(v) == PlanFile {
			return true
		}
	}
	return false
}

// planAsks is the plan's questions: every PLAN.md line that starts with `Q:`.
func planAsks(jobDir string) []string {
	raw, err := os.ReadFile(filepath.Join(jobDir, PlanFile))
	if err != nil {
		return nil
	}
	var asks []string
	for _, l := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "Q:") {
			asks = append(asks, t)
		}
	}
	return asks
}
