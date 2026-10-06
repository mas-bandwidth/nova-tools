package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const strangerSprintPath = "../../docs/stranger/three-card-sprint.md"

// strangerSections are the four sections a cold stranger run is recorded under.
var strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}

// strangerCardRE is a stumble's proposed card line: `card: <id>`, as a list item or not.
var strangerCardRE = regexp.MustCompile(`^(?:- )?card: [A-Za-z0-9][A-Za-z0-9._-]*\s*$`)

// strangerSectionBodies splits a run record into its `## ` sections, by heading.
func strangerSectionBodies(text string) map[string][]string {
	bodies := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(text, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			cur = strings.TrimSpace(h)
			bodies[cur] = []string{}
			continue
		}
		if cur != "" {
			bodies[cur] = append(bodies[cur], line)
		}
	}
	return bodies
}

// strangerRunProblems is every way a run record falls short: a missing section,
// a transcript with no command line (`$ <command>`), and a stumble (a `### `
// heading under Stumbles) with no proposed card line.
func strangerRunProblems(text string) []string {
	var problems []string
	bodies := strangerSectionBodies(text)
	for _, s := range strangerSections {
		if _, ok := bodies[s]; !ok {
			problems = append(problems, "no ## "+s+" section")
		}
	}
	commands := 0
	for _, line := range bodies["Transcript"] {
		if strings.HasPrefix(line, "$ ") && strings.TrimSpace(line) != "$" {
			commands++
		}
	}
	if _, ok := bodies["Transcript"]; ok && commands == 0 {
		problems = append(problems, "the transcript has no command line")
	}
	stumble, carded := "", false
	closeStumble := func() {
		if stumble != "" && !carded {
			problems = append(problems, "stumble "+quoteStumble(stumble)+" has no proposed card line `card: <id>`")
		}
	}
	for _, line := range bodies["Stumbles"] {
		if h, ok := strings.CutPrefix(line, "### "); ok {
			closeStumble()
			stumble, carded = strings.TrimSpace(h), false
			continue
		}
		if strangerCardRE.MatchString(strings.TrimSpace(line)) {
			carded = true
		}
	}
	closeStumble()
	return problems
}

// quoteStumble quotes a stumble heading for a problem line.
func quoteStumble(s string) string { return `"` + s + `"` }

// TestStrangerRunThreeCardSprintIsRecorded holds docs/stranger/three-card-sprint.md,
// the cold run of a three-card sprint, to its shape: the four sections, a
// transcript of real commands, and a proposed card for every stumble. The
// shapes it refuses are pinned on fakes first.
func TestStrangerRunThreeCardSprintIsRecorded(t *testing.T) {
	t.Parallel()

	good := "# run\n\n## Setup\n\nbench\n\n## Transcript\n\n```text\n$ nova-sprint version\nnova-sprint devel\n```\n\n" +
		"## Stumbles\n\n### 1. one\n\n- card: a-card\n\n### 2. two\n\ncard: b-card\n\n## Verdict\n\nyes\n"
	require.Empty(t, strangerRunProblems(good), "the fake good record")

	for name, c := range map[string]struct{ text, want string }{
		"no Verdict":    {strings.Replace(good, "## Verdict", "## Ending", 1), "no ## Verdict section"},
		"no Setup":      {strings.Replace(good, "## Setup", "Setup", 1), "no ## Setup section"},
		"no command":    {strings.Replace(good, "$ nova-sprint version", "nova-sprint version", 1), "the transcript has no command line"},
		"uncarded":      {strings.Replace(good, "card: b-card", "a card is owed", 1), `stumble "2. two" has no proposed card line`},
		"empty card id": {strings.Replace(good, "- card: a-card", "- card: ", 1), `stumble "1. one" has no proposed card line`},
	} {
		got := strings.Join(strangerRunProblems(c.text), "\n")
		require.Contains(t, got, c.want, "%s: the fake must be refused", name)
	}

	raw, err := os.ReadFile(strangerSprintPath)
	require.NoError(t, err, "reading %s", strangerSprintPath)
	text := string(raw)
	require.Empty(t, strangerRunProblems(text), "%s", strangerSprintPath)
	require.Contains(t, strangerSectionBodies(text), "Stumbles")
}
