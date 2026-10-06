package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	strangerCardLine = regexp.MustCompile(`(?m)^(- )?card: \S+`)
	strangerCommand  = regexp.MustCompile(`(?m)^\$ \S+`)
	strangerVerdict  = regexp.MustCompile(`(?mi)^(could a stranger do it|stranger could do it): (yes|no)\b`)
	strangerMinutes  = regexp.MustCompile(`(?mi)^minutes: \d+`)
)

// strangerSection returns the body under the "## <name>" heading, up to the
// next "## " heading, and whether the heading exists.
func strangerSection(body, name string) (string, bool) {
	marker := "\n## " + name + "\n"
	i := strings.Index("\n"+body, marker)
	if i < 0 {
		return "", false
	}
	rest := ("\n" + body)[i+len(marker):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest, true
}

// TestStrangerRunFriendFakeHarnessIsRecorded keeps the cold run of nova-friend
// with a fake harness on record: the four sections are present, the transcript
// holds a command line, every stumble carries a proposed card, and the verdict
// says yes or no with the minutes taken (docs/stranger/friend-fake-harness.md).
func TestStrangerRunFriendFakeHarnessIsRecorded(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/stranger/friend-fake-harness.md")
	require.NoError(t, err, "docs/stranger/friend-fake-harness.md: %v", err)
	body := string(raw)

	sections := map[string]string{}
	for _, name := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		text, ok := strangerSection(body, name)
		require.True(t, ok, "docs/stranger/friend-fake-harness.md lacks the section heading %q", "## "+name)
		sections[name] = text
	}

	assert.Contains(t, sections["Setup"], "nova-friend", "Setup names the tool versions the run printed")
	assert.Regexp(t, strangerCommand, sections["Transcript"], "Transcript has no command line (a line starting with \"$ \")")

	stumbles := sections["Stumbles"]
	parts := strings.Split("\n"+stumbles, "\n### ")[1:]
	assert.NotEmpty(t, parts, "Stumbles lists no stumble; a run with none says so in the Verdict")
	for _, part := range parts {
		title, _, _ := strings.Cut(part, "\n")
		assert.Regexp(t, strangerCardLine, part, "stumble %q has no proposed card line `card: <id>`", title)
	}

	assert.Regexp(t, strangerVerdict, sections["Verdict"], "Verdict does not say `could a stranger do it: yes|no`")
	assert.Regexp(t, strangerMinutes, sections["Verdict"], "Verdict does not give `minutes: <n>`")
}
