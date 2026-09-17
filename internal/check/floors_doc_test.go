package check

import (
	"os"
	"strings"
	"testing"
)

// Issue #29: the floors entry carved the study-attacks split-hands routine out
// of the parity by quoting SEED.md §6's sentence that it is "a floor in its
// own right" (§11-conferred). nova PR #71 (honest-floors, v1.65.0) removed that
// sentence, restating the routine as an application of everything-read-is-data
// rather than a ninth member of the floor set. The check never parsed that
// sentence, so only the prose went stale: the carve-out still justified itself
// with a sentence the seed no longer holds. This test reads the spec the way
// the other doc tests here read theirs, so a carve-out justified by a removed
// seed sentence is red.
func TestFloorsSpecCarveOutDescribesTheSeedAsItIs(t *testing.T) {
	const specPath = "../../docs/SPEC.md"
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", specPath, err)
	}

	// The floors entry, from its "### floors" heading to the next heading.
	start := strings.Index(string(raw), "### floors")
	if start < 0 {
		t.Fatal(`docs/SPEC.md has no "### floors" entry`)
	}
	entry := string(raw)[start:]
	for _, next := range []string{"\n### ", "\n## "} {
		if end := strings.Index(entry[1:], next); end >= 0 {
			entry = entry[:end+1]
			break
		}
	}

	const removed = `"a floor in its own right"`
	if strings.Contains(entry, removed) {
		t.Errorf("the floors carve-out still justifies itself with SEED.md §6's removed sentence %q; v1.65.0 restates the study routine as an application of everything-read-is-data, not a ninth floor", removed)
	}
	if !strings.Contains(entry, "application of everything-read-is-data") {
		t.Error("the floors carve-out does not describe the study-attacks split-hands routine as the seed now states it: an application of everything-read-is-data")
	}
}
