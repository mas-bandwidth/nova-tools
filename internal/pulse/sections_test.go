package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecPulseSectionsSplitIntoFiles holds issue #560: one file per spec
// section so parallel edits stop colliding in one 700-line SPEC-PULSE.md.
// Each section lives under docs/spec-pulse/<slug>.md and is listed by the top
// file docs/SPEC-PULSE.md.
func TestSpecPulseSectionsSplitIntoFiles(t *testing.T) {
	sections := []struct{ slug, heading string }{
		{"the-loop-in-words", "## The loop, in words"},
		{"the-rules-numbered", "## The rules, numbered"},
		{"the-verbs", "## The verbs"},
		{"handoff", "## Handoff"},
		{"status", "## Status"},
		{"progress", "## Progress"},
		{"rate-and-convergence", "## Rate and convergence"},
		{"exit-codes-and-the-output-grammar", "## Exit codes and the output grammar"},
		{"the-card-as-cut-writes-it", "## The card, as `cut` writes it"},
		{"what-this-draft-does-not-do", "## What this draft does not do"},
		{"the-manager-tier", "## The manager tier"},
		{"tests-this-spec-demands", "## Tests this spec demands"},
		{"open-questions", "## Open questions"},
	}

	topRaw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatalf("SPEC-PULSE.md is the top file: %v", err)
	}
	top := string(topRaw)

	for _, s := range sections {
		p := filepath.Join("..", "..", "docs", "spec-pulse", s.slug+".md")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: section file missing: %v", s.slug, err)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(string(b)), s.heading) {
			t.Errorf("%s: section file must open with %q", s.slug, s.heading)
		}
		if !strings.Contains(top, "spec-pulse/"+s.slug+".md") {
			t.Errorf("%s: SPEC-PULSE.md does not list its section file", s.slug)
		}
	}
}
