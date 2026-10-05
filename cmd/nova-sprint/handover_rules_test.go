package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runbookRules reads the rules table of docs/SPRINT-COORDINATOR.md, section
// 10, as rows of six cells (internal/docs/coordrules_test.go checks the verbs
// they name).
func runbookRules(t *testing.T) []coordRule {
	t.Helper()
	raw, err := os.ReadFile("../../docs/SPRINT-COORDINATOR.md")
	require.NoError(t, err)
	var rules []coordRule
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "| # | Rule | Why | Does it | Card | Pass |" {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		require.Len(t, cells, 6, "a rule row has six cells: %s", line)
		require.Equal(t, strconv.Itoa(len(rules)+1), cells[0], "rules are numbered in order: %s", line)
		rules = append(rules, coordRule{Rule: cells[1], Why: cells[2], Does: cells[3], Card: cells[4], Pass: cells[5] == "yes"})
	}
	require.True(t, in, "docs/SPRINT-COORDINATOR.md has no rules table")
	return rules
}

// TestHandoverRulesAreTheRunbooksRules holds the rules handover prints and the
// runbook's table to one list: a rule changed in one and not the other fails
// here, with the row to write.
func TestHandoverRulesAreTheRunbooksRules(t *testing.T) {
	t.Parallel()
	doc := runbookRules(t)
	require.NotEmpty(t, coordinatorRules)
	var missing []string
	for i := len(doc); i < len(coordinatorRules); i++ {
		missing = append(missing, coordinatorRules[i].row(i+1))
	}
	assert.Empty(t, missing, "docs/SPRINT-COORDINATOR.md lacks these rows of handover_rules.go:\n%s", strings.Join(missing, "\n"))
	for i := range min(len(doc), len(coordinatorRules)) {
		assert.Equal(t, coordinatorRules[i].row(i+1), doc[i].row(i+1), "rule %d differs between handover_rules.go and the runbook", i+1)
	}
	assert.Len(t, doc, len(coordinatorRules), "the runbook and handover_rules.go hold different counts of rules")
}

// TestHandoverPrintsTheCoordinatorRules: the text prints every pass rule with
// its number and what does it, still in one screen, and names where the rest
// are; --json carries every rule.
func TestHandoverPrintsTheCoordinatorRules(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --owner owner")

	out := ta.ok("handover")
	pass := 0
	for i, r := range coordinatorRules {
		line := "RULE " + r.line(i+1) + "\n"
		if r.Pass {
			pass++
			assert.Contains(t, out, line, out)
		} else {
			assert.NotContains(t, out, line, out)
		}
	}
	require.NotZero(t, pass, "no rule is printed in the text")
	assert.Contains(t, out, "RULE "+handoverWaves+"\n", "the waves rule stays")
	assert.Contains(t, out, "RULES "+strconv.Itoa(len(coordinatorRules))+" numbered: "+strconv.Itoa(pass)+" above; every one in handover --json and docs/SPRINT-COORDINATOR.md section 10\n", out)
	assert.Less(t, strings.Count(out, "\n"), 60, "one screen:\n%s", out)

	var h struct {
		Rules []string `json:"rules"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("handover --json")), &h))
	require.Len(t, h.Rules, len(coordinatorRules)+1, "the waves rule and every numbered rule")
	for i, r := range coordinatorRules {
		assert.Equal(t, r.line(i+1), h.Rules[i+1])
	}
}
