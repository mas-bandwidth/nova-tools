package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ISSUE #74 (Glenn, 2026-09-12): tonight's read-pr reports through nova-swarm were
// correct and long -- narration of the clone, restated tasks, a "what worked"
// section, summary paragraphs -- and the table grammar broke twice on pipes inside
// backticks (D12). The ask is an ADDITIVE condition on `nova-swarm template --name
// read-pr`: findings only, one bounded line per finding, a bounded report, no pipe
// inside backticks, the verdict line last, `findings: 0` when there is nothing.
//
// The template is text the tool prints and the spec is the contract that text
// serves. This test pins both, so the ask cannot drift out of one of them.
func TestReadPRTemplateAsksForBoundedFindingsOnly(t *testing.T) {
	rules := []string{
		"findings only",
		"one line per finding",
		"twelve words",
		"under 40 lines",
		"under 300 characters",
		"no pipe inside backticks",
		"verdict line last",
	}
	body, err := Template("read-pr")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if !strings.Contains(strings.ToLower(body), rule) {
			t.Errorf("the read-pr template does not ask for %q", rule)
		}
	}

	// AND THE SPEC SAYS THE SAME. The read-pr section is the read-pr template's own
	// contract, and a condition that lives in the binary but not in the spec is a
	// condition the next reader cannot review.
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("the template's contract is the spec's: %s", err)
	}
	section := readPRSpecSection(t, string(raw))
	for _, rule := range rules {
		if !strings.Contains(strings.ToLower(section), rule) {
			t.Errorf("SPEC-SWARM.md's read-pr section does not ask for %q", rule)
		}
	}
}

// readPRSpecSection is the read-pr heading and everything under it, to the next
// heading, which is where the read-pr conditions are written.
func readPRSpecSection(t *testing.T, spec string) string {
	t.Helper()
	lines := strings.Split(spec, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "### `read-pr`") {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "### ") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	t.Fatal("SPEC-SWARM.md has no read-pr section")
	return ""
}
