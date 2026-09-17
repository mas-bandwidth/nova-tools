package specwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/SPEC-WORK.md carries the duty-tier amendment (nova-tools#500) as one named
// section of numbered rules, each with the hurt that made it and its listed red
// test (a Replay name). This test reads the spec the way
// TestSpecDecideNamesTwelveRules reads SPEC-DECIDE.md, so the section or a rule
// renamed out of the doc is red.
func TestSpecWorkDutyTierSectionNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-WORK.md"))
	if err != nil {
		t.Fatalf("the duty-tier amendment's spec is missing: %s", err)
	}
	doc := string(raw)

	const heading = "## The duty tier and the single-writer kernel"
	if !strings.Contains(doc, heading) {
		t.Fatalf("docs/SPEC-WORK.md does not carry the #500 section %q", heading)
	}
	if !strings.Contains(doc, "#500") {
		t.Errorf("docs/SPEC-WORK.md does not mark the duty-tier amendment #500")
	}

	for _, rule := range []string{
		"duty-tier-executes-the-policy",
		"escalation-carries-rule-default-age",
		"wait-table-four-presence-columns",
		"quiet-time-calls-nothing",
		"cost-per-accepted-decision",
		"single-writer-kernel-total-order",
	} {
		if !strings.Contains(doc, rule) {
			t.Errorf("docs/SPEC-WORK.md does not list the duty-tier rule whose red test is %q", rule)
		}
	}

	// Rule 3 of the amendment: the per-harness wait table of Presence gains the
	// four presence-fact columns. The header is the contract the rule names.
	const waitHeader = "| harness | what holds the wait |"
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, waitHeader) {
			for _, fact := range []string{"process alive", "beat written", "delivery handled", "parent woke"} {
				if !strings.Contains(line, fact) {
					t.Errorf("the Presence wait table does not carry the presence-fact column %q (#500 rule 3):\n%s", fact, line)
				}
			}
			return
		}
	}
	t.Errorf("docs/SPEC-WORK.md does not carry the per-harness wait table of Presence")
}
