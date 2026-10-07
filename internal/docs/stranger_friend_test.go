package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const strangerFriendRecord = "../../docs/stranger/friend-fake-harness.md"

var strangerCardLine = regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9.-]*$`)

// strangerSection returns the text under the "## name" heading, up to the next
// "## " heading, and whether the heading is there.
func strangerSection(body, name string) (string, bool) {
	_, rest, ok := strings.Cut("\n"+body, "\n## "+name+"\n")
	if !ok {
		return "", false
	}
	text, _, _ := strings.Cut(rest, "\n## ")
	return text, true
}

// TestStrangerRunFriendFakeHarnessIsRecorded holds the record of the cold
// stranger run of nova-friend with a fake harness to its shape: the four
// sections, a transcript with at least one command line, and a proposed card
// under every stumble, so the coordinator can turn each one into a card.
func TestStrangerRunFriendFakeHarnessIsRecorded(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(strangerFriendRecord)
	require.NoError(t, err, "the run is recorded in docs/stranger/friend-fake-harness.md")
	body := string(raw)

	sections := map[string]string{}
	for _, name := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		text, ok := strangerSection(body, name)
		require.True(t, ok, "docs/stranger/friend-fake-harness.md lacks the section heading %q", "## "+name)
		sections[name] = text
	}

	assert.Regexp(t, `(?m)^\$ \S`, sections["Transcript"], "the transcript has no command line (a line starting with `$ `)")
	assert.Contains(t, sections["Setup"], "version", "the setup names the versions `<tool> version` printed")
	assert.Regexp(t, `(?mi)^could a stranger do it: (yes|no)\b`, sections["Verdict"], "the verdict says whether a stranger could do it")
	assert.Regexp(t, `(?i)\d+ minutes`, sections["Verdict"], "the verdict says the minutes taken")

	stumbles := strings.Split("\n"+sections["Stumbles"], "\n### ")[1:]
	for _, stumble := range stumbles {
		title, _, _ := strings.Cut(stumble, "\n")
		assert.Regexp(t, strangerCardLine, stumble, "stumble %q has no proposed card line `card: <id>`", title)
		assert.Contains(t, stumble, "paths:", "stumble %q has no proposed PATHS", title)
	}
}
