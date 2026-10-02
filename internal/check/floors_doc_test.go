package check

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The floors entry in docs/SPEC.md carves the study-attacks split-hands routine
// out of the parity. The seed states that routine as an application of
// everything-read-is-data, not as a ninth member of the floor set, and no
// longer holds the sentence calling it "a floor in its own right". The check
// never parses that sentence, so only the spec prose can go stale. This test
// reads the spec the way the other doc tests here read theirs, so a carve-out
// justified by the removed seed sentence is red.
func TestFloorsSpecCarveOutDescribesTheSeedAsItIs(t *testing.T) {
	t.Parallel()

	const specPath = "../../docs/SPEC.md"
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err, "cannot read %s", specPath)

	// The floors entry, from its "### floors" heading to the next heading.
	start := strings.Index(string(raw), "### floors")
	require.GreaterOrEqual(t, start, 0, `docs/SPEC.md has no "### floors" entry`)
	entry := string(raw)[start:]
	for _, next := range []string{"\n### ", "\n## "} {
		if end := strings.Index(entry[1:], next); end >= 0 {
			entry = entry[:end+1]
			break
		}
	}

	const removed = `"a floor in its own right"`
	assert.NotContains(t, entry, removed, "the floors carve-out still justifies itself with SEED.md §6's removed sentence %q; v1.65.0 restates the study routine as an application of everything-read-is-data, not a ninth floor", removed)
	assert.Contains(t, entry, "application of everything-read-is-data", "the floors carve-out does not describe the study-attacks split-hands routine as the seed now states it: an application of everything-read-is-data")
}
