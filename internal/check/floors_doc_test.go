package check

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The floors spec describes the study-attacks split-hands routine as an
// application of everything-read-is-data, not a ninth floor. The check does
// not parse that routine, so this test guards the spec's explanation.
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
	assert.NotContains(t, entry, removed, "the floors carve-out uses %q; the study routine is an application of everything-read-is-data, not a ninth floor", removed)
	assert.Contains(t, entry, "application of everything-read-is-data", "the floors carve-out does not describe the study-attacks split-hands routine as the seed states it: an application of everything-read-is-data")
}
