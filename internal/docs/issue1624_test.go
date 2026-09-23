package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue1624 proves that docs/SPEC-DECIDE.md carries the H3 housekeeping
// section: latency and wall clock in every decision row and on every line
// (nova-tools#1624, H3, SPEC-AHEAD: #1624).
//
// The spec must name ms and wall_ms as fields on every route and classify log
// row, the per-counter presence rule (absent, never zero), ms= on the ROUTE
// and CLASSIFY lines, and median and p95 per question and per decider in
// log --summary.
func TestIssue1624(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "docs", "SPEC-DECIDE.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	content := string(body)

	// H3 is the housekeeping section: Latency and wall clock.
	// Extract the section between "### Housekeeping" and the next "###" heading.
	section, rest, found := strings.Cut(content, "### Housekeeping")
	if !found {
		t.Fatal("docs/SPEC-DECIDE.md has no '### Housekeeping' section; H3 lives there")
	}
	// Get everything up to the next top-level subsection.
	nextIdx := strings.Index(rest, "\n### ")
	if nextIdx < 0 {
		// No next section found; use the rest as-is.
		nextIdx = len(rest)
	}
	section = "### Housekeeping" + rest[:nextIdx]

	for _, want := range []struct {
		phrase string
		why    string
	}{
		{"H3", "the housekeeping rule number, so H1-H4 are all present"},
		{"#1624", "the issue this section is SPEC-AHEAD for, so the amendment is tracked"},
		{"`ms`", "the provider round-trip latency field must be named"},
		{"`wall_ms`", "the verb-start-to-line wall clock field must be named"},
		{"absent", "the per-counter presence rule: unmeasured counters are absent, not zero"},
		{"never zero", "the per-counter presence rule: an absence is not a zero"},
		{"`ROUTE`", "the route output line must carry latency"},
		{"`CLASSIFY`", "the classify output line must carry latency"},
		{"median", "log --summary must report median latency"},
		{"p95", "log --summary must report the 95th percentile latency"},
		{"per question", "latency percentiles must be reported per question"},
		{"per decider", "latency percentiles must be reported per decider"},
		{"monotonic", "ms is measured on a monotonic clock around the call alone"},
	} {
		if !strings.Contains(section, want.phrase) {
			t.Errorf("H3 housekeeping missing %q; %s", want.phrase, want.why)
		}
	}
}
