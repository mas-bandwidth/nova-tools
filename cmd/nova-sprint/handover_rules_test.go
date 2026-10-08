package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runbookRules reads the numbered rules of docs/SPRINT-COORDINATOR.md section
// 10, each sentence joined from the lines it wraps over (the shape
// internal/docs TestEveryCoordinatorRuleNamesVerbsThatExist checks).
func runbookRules(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../docs/SPRINT-COORDINATOR.md")
	require.NoError(t, err)
	head := regexp.MustCompile(`^R([0-9]+)\. \*\*(.+)$`)
	var rules []string
	in, open := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = line == "## 10. The rules"
			continue
		}
		switch {
		case in && open:
			rules[len(rules)-1] += " " + strings.TrimSpace(line)
		case in && head.MatchString(line):
			rules = append(rules, head.FindStringSubmatch(line)[2])
		default:
			continue
		}
		last := rules[len(rules)-1]
		open = !strings.HasSuffix(last, "**")
		rules[len(rules)-1] = strings.TrimSuffix(last, "**")
	}
	return rules
}

// The rules handover prints are the runbook's, in its order: a rule changed
// in one and not the other fails here.
func TestHandoverRulesAreTheRunbooks(t *testing.T) {
	t.Parallel()
	doc := runbookRules(t)
	require.NotEmpty(t, doc, "docs/SPRINT-COORDINATOR.md section 10 holds no rule")
	assert.Equal(t, doc, coordinatorRules, "cmd/nova-sprint/handover_rules.go and docs/SPRINT-COORDINATOR.md section 10 disagree")
}

// Each rule is one RULE line of the handover, numbered as the runbook numbers it.
func TestHandoverTextPrintsEachRuleByNumber(t *testing.T) {
	t.Parallel()
	lines := handoverRuleLines()
	require.Len(t, lines, len(coordinatorRules))
	assert.Equal(t, "R1. "+coordinatorRules[0], lines[0])
	assert.Equal(t, "R"+strconv.Itoa(len(lines))+". "+coordinatorRules[len(lines)-1], lines[len(lines)-1])

	ta := newTestApp(t)
	out := ta.a.handoverText(handoverView{Seat: seatView{Holder: "coordinator"}, Rules: lines})
	for _, l := range lines {
		assert.Contains(t, out, "RULE "+l+"\n")
	}
	assert.Equal(t, len(lines), strings.Count(out, "\nRULE R"), out)
}

// handoverRuleLines is each rule as its RULE line carries it: "R<n>. <sentence>".
// It is the test's, not the binary's: handover's own view builds the lines from
// coordinatorRules (seat.go).
func handoverRuleLines() []string {
	lines := make([]string, len(coordinatorRules))
	for i, r := range coordinatorRules {
		lines[i] = "R" + strconv.Itoa(i+1) + ". " + r
	}
	return lines
}
