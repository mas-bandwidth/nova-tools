package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const strangerSprintPath = "../../docs/stranger/three-card-sprint.md"

// strangerSections are the four headings a stranger run is recorded under: what
// was run where, every command with its output, each stumble with the card that
// fixes it, and whether a stranger could do it.
var strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}

// strangerCardLine is the proposed card a stumble carries, the line the
// coordinator turns into a card.
var strangerCardLine = regexp.MustCompile("(?m)^- card: `?[a-z0-9][a-z0-9.-]*`?")

// TestStrangerRunThreeCardSprintIsRecorded holds the record of a cold stranger
// run of a three-card nova-sprint: the four sections are there, the transcript
// carries the commands that were run, and every stumble proposes its card.
func TestStrangerRunThreeCardSprintIsRecorded(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(strangerSprintPath)
	require.NoError(t, err, "%s: %v", strangerSprintPath, err)
	sections := splitH2(string(raw))

	for _, name := range strangerSections {
		_, ok := sections[name]
		assert.True(t, ok, "%s has no `## %s` section; a stranger run is recorded under Setup, Transcript, Stumbles and Verdict", strangerSprintPath, name)
	}

	commands := 0
	for _, line := range strings.Split(sections["Transcript"], "\n") {
		if strings.HasPrefix(line, "$ ") {
			commands++
		}
	}
	assert.Positive(t, commands, "%s: the Transcript has no command line (`$ <command>`); it records every command the run typed", strangerSprintPath)

	stumbles := splitH3(sections["Stumbles"])
	assert.NotEmpty(t, stumbles, "%s: Stumbles lists no `### ` stumble; a run with none says so under Verdict and keeps one heading per stumble here", strangerSprintPath)
	for title, body := range stumbles {
		assert.Regexp(t, strangerCardLine, body, "%s: stumble %q has no proposed card line `- card: <id>`", strangerSprintPath, title)
	}
}

// splitH2 is the bodies of a document's `## ` sections, by heading.
func splitH2(doc string) map[string]string { return splitHeadings(doc, "## ") }

// splitH3 is the bodies of a section's `### ` subsections, by heading.
func splitH3(doc string) map[string]string { return splitHeadings(doc, "### ") }

func splitHeadings(doc, prefix string) map[string]string {
	out := map[string]string{}
	name, fenced := "", false
	var body strings.Builder
	flush := func() {
		if name != "" {
			out[name] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if rest, ok := strings.CutPrefix(line, prefix); ok && !fenced {
			flush()
			name = strings.TrimSpace(rest)
			continue
		}
		body.WriteString(line + "\n")
	}
	flush()
	return out
}
