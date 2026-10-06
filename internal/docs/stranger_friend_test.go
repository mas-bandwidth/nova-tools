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
	strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}
	strangerCardLine = regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9-]*\s*$`)
)

// strangerSection returns the text under the "## <name>" heading up to the
// next "## " heading, and whether the heading is there.
func strangerSection(body, name string) (string, bool) {
	_, rest, ok := strings.Cut("\n"+body, "\n## "+name+"\n")
	if !ok {
		return "", false
	}
	text, _, _ := strings.Cut(rest, "\n## ")
	return text, true
}

// strangerProblems lists what is missing from a recorded stranger run: one of
// the four sections, a command line in the transcript, or a proposed card
// (`card: <id>`) under a stumble.
func strangerProblems(body string) []string {
	var problems []string
	sections := map[string]string{}
	for _, name := range strangerSections {
		text, ok := strangerSection(body, name)
		if !ok {
			problems = append(problems, "missing section ## "+name)
			continue
		}
		sections[name] = text
	}
	if text, ok := sections["Transcript"]; ok && !regexp.MustCompile(`(?m)^\$ \S`).MatchString(text) {
		problems = append(problems, "the transcript has no command line (a line starting \"$ \")")
	}
	if text, ok := sections["Stumbles"]; ok {
		for _, stumble := range strings.Split("\n"+text, "\n### ")[1:] {
			title, _, _ := strings.Cut(stumble, "\n")
			if !strangerCardLine.MatchString(stumble) {
				problems = append(problems, "stumble \""+title+"\" has no proposed card line `card: <id>`")
			}
		}
	}
	return problems
}

func TestStrangerRunFriendFakeHarnessIsRecorded(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/stranger/friend-fake-harness.md")
	require.NoError(t, err, "docs/stranger/friend-fake-harness.md is the recorded cold run")
	assert.Empty(t, strangerProblems(string(body)))
}

func TestStrangerRunRecordRefusesWhatIsMissing(t *testing.T) {
	t.Parallel()

	whole := "## Setup\nbench\n## Transcript\n$ nova-friend version\nok\n## Stumbles\n### One\ncard: fix-one\n## Verdict\nyes\n"
	require.Empty(t, strangerProblems(whole))

	for _, name := range strangerSections {
		without := strings.Replace(whole, "## "+name+"\n", "## Other\n", 1)
		assert.Contains(t, strangerProblems(without), "missing section ## "+name)
	}
	noCommand := strings.Replace(whole, "$ nova-friend version", "nova-friend version", 1)
	assert.Contains(t, strangerProblems(noCommand), "the transcript has no command line (a line starting \"$ \")")
	noCard := strings.Replace(whole, "card: fix-one", "a card would help", 1)
	assert.Equal(t, []string{"stumble \"One\" has no proposed card line `card: <id>`"}, strangerProblems(noCard))
}
