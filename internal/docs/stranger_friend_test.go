package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// section returns the text under "## name" up to the next "## " heading.
func section(page, name string) (string, bool) {
	_, rest, ok := strings.Cut(page, "\n## "+name+"\n")
	if !ok {
		return "", false
	}
	body, _, _ := strings.Cut(rest, "\n## ")
	return body, true
}

// The cold stranger run of nova-friend with a fake harness is a record the
// coordinator turns into cards: four sections, a transcript that shows the
// commands that were run, and a proposed card for every stumble.
func TestStrangerRunFriendFakeHarnessIsRecorded(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(testRoot(t), "docs", "stranger", "friend-fake-harness.md"))
	require.NoError(t, err)
	page := string(raw)

	sections := map[string]string{}
	for _, name := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		body, ok := section(page, name)
		require.True(t, ok, "missing section ## %s", name)
		sections[name] = body
	}

	assert.Regexp(t, regexp.MustCompile(`(?m)^\$ \S+`), sections["Transcript"], "the transcript has no command line")

	stumbles := regexp.MustCompile(`(?m)^### `).Split(sections["Stumbles"], -1)[1:]
	for _, s := range stumbles {
		title, _, _ := strings.Cut(s, "\n")
		assert.Regexp(t, regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9-]*`), s, "stumble %q has no proposed card line", title)
	}

	assert.Regexp(t, regexp.MustCompile(`(?mi)^could a stranger do it: (yes|no)`), sections["Verdict"], "the verdict does not say yes or no")
}
