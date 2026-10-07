package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const strangerBusRecord = "../../docs/stranger/bus-two-names.md"

// TestStrangerRunBusTwoNamesIsRecorded holds docs/stranger/bus-two-names.md,
// the cold stranger run of nova-bus between two names, to its shape: the four
// sections, a transcript of real commands, and a proposed card for every
// stumble. The shapes it refuses are pinned on fakes first.
func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()

	good := "# run\n\n## Setup\n\nbench\n\n## Transcript\n\n```text\n$ nova-bus version\nnova-bus devel\n```\n\n" +
		"## Stumbles\n\n### 1. one\n\n- card: a-card\n\n### 2. two\n\ncard: b-card\n\n## Verdict\n\nyes\n"
	require.Empty(t, strangerRunProblems(good), "the fake good record")

	for name, c := range map[string]struct{ text, want string }{
		"no Verdict": {strings.Replace(good, "## Verdict", "## Ending", 1), "no ## Verdict section"},
		"no Setup":   {strings.Replace(good, "## Setup", "Setup", 1), "no ## Setup section"},
		"no command": {strings.Replace(good, "$ nova-bus version", "nova-bus version", 1), "the transcript has no command line"},
		"uncarded":   {strings.Replace(good, "card: b-card", "a card is owed", 1), `stumble "2. two" has no proposed card line`},
	} {
		got := strings.Join(strangerRunProblems(c.text), "\n")
		require.Contains(t, got, c.want, "%s: the fake must be refused", name)
	}

	raw, err := os.ReadFile(strangerBusRecord)
	require.NoError(t, err, "reading %s", strangerBusRecord)
	text := string(raw)
	require.Empty(t, strangerRunProblems(text), "%s", strangerBusRecord)
	require.Contains(t, strangerSectionBodies(text), "Stumbles")
}
