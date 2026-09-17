package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-PULSE.md's "The loop as a tool (pit stop 3)" section is the bug table
// (#828) turned into rules: thirteen bugs, one class each, one rule per class,
// each rule quoting the hurt that made it and the test that proves it. A spec
// nobody reads in a build rots, so this test reads the section the way
// internal/decide's doc test reads SPEC-DECIDE.md: rename a class out of the
// doc and the build is red.
func TestSpecPulseNamesThePitStop3Classes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatalf("SPEC-PULSE.md is missing: %s", err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"## The loop as a tool (pit stop 3)",
		"A. State in env vars and restarts",
		"B. One fact written twice",
		"C. All-or-nothing STOP",
		"D. Point-in-time verdicts",
		"E. No reaper",
		"F. The coordinator's hands",
		"G. The coordinator's own tokens",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-PULSE.md does not name the pit stop 3 rule keyed by %q", phrase)
		}
	}
}
