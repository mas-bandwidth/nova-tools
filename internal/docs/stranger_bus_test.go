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

// strangerBusFile is the record of a cold run of nova-bus between two names.
const strangerBusFile = "docs/stranger/bus-two-names.md"

var (
	strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}
	strangerCommand  = regexp.MustCompile(`(?m)^\$ \S`)
	strangerCardLine = regexp.MustCompile(`(?m)^(- )?card: [a-z0-9][a-z0-9-]*\s*$`)
	strangerVerdict  = regexp.MustCompile(`(?mi)^could a stranger do it: (yes|no)\b`)
	strangerMinutes  = regexp.MustCompile(`(?mi)^minutes taken: \d+`)
)

// strangerSection is the body of the "## <name>" section of page, up to the
// next "## " heading; ok is false when the heading is missing.
func strangerSection(page, name string) (body string, ok bool) {
	_, rest, found := strings.Cut("\n"+page, "\n## "+name+"\n")
	if !found {
		return "", false
	}
	body, _, _ = strings.Cut(rest, "\n## ")
	return body, true
}

// checkStrangerRun names every way a stranger-run record falls short: a
// missing section, a transcript with no command line, a stumble with no
// proposed card line, a verdict that does not answer.
func checkStrangerRun(page string) []string {
	var problems []string
	sections := map[string]string{}
	for _, name := range strangerSections {
		body, ok := strangerSection(page, name)
		if !ok {
			problems = append(problems, "missing section: ## "+name)
			continue
		}
		sections[name] = body
	}
	if body, ok := sections["Transcript"]; ok && !strangerCommand.MatchString(body) {
		problems = append(problems, "the Transcript has no command line (a line starting with \"$ \")")
	}
	if body, ok := sections["Stumbles"]; ok {
		stumbles := strings.Split("\n"+body, "\n### ")[1:]
		if len(stumbles) == 0 {
			problems = append(problems, "the Stumbles section has no stumble (### <title>), and no line saying there were none")
		}
		for _, s := range stumbles {
			title, _, _ := strings.Cut(s, "\n")
			if !strangerCardLine.MatchString(s) {
				problems = append(problems, "stumble has no proposed card line `card: <id>`: "+title)
			}
		}
	}
	if body, ok := sections["Verdict"]; ok {
		if !strangerVerdict.MatchString(body) {
			problems = append(problems, "the Verdict does not say `could a stranger do it: yes|no`")
		}
		if !strangerMinutes.MatchString(body) {
			problems = append(problems, "the Verdict does not say `minutes taken: <n>`")
		}
	}
	return problems
}

const strangerGood = "# run\n\n## Setup\n\nbench\n\n## Transcript\n\n```text\n$ nova-bus version\nnova-bus 1\n```\n\n## Stumbles\n\n### one\n\nwhat\n\ncard: bus-one\n\n## Verdict\n\ncould a stranger do it: no\nminutes taken: 9\n"

// The checker is red for each shortfall the card names, on fakes, and green on
// a record that has none.
func TestStrangerRunCheckerNamesEveryShortfall(t *testing.T) {
	t.Parallel()
	assert.Empty(t, checkStrangerRun(strangerGood))

	for _, name := range strangerSections {
		t.Run("missing "+name, func(t *testing.T) {
			t.Parallel()
			page := strings.Replace(strangerGood, "\n## "+name+"\n", "\n## Other\n", 1)
			assert.Contains(t, checkStrangerRun(page), "missing section: ## "+name)
		})
	}
	t.Run("no command line", func(t *testing.T) {
		t.Parallel()
		page := strings.Replace(strangerGood, "$ nova-bus version", "nova-bus version", 1)
		assert.Len(t, checkStrangerRun(page), 1)
	})
	t.Run("stumble without a card", func(t *testing.T) {
		t.Parallel()
		page := strings.Replace(strangerGood, "card: bus-one\n", "", 1)
		problems := checkStrangerRun(page)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0], "one")
	})
	t.Run("one of two stumbles without a card", func(t *testing.T) {
		t.Parallel()
		page := strings.Replace(strangerGood, "## Verdict", "### two\n\nwhat\n\n## Verdict", 1)
		problems := checkStrangerRun(page)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0], "two")
	})
	t.Run("verdict without an answer", func(t *testing.T) {
		t.Parallel()
		page := strings.Replace(strangerGood, "could a stranger do it: no\nminutes taken: 9\n", "fine\n", 1)
		assert.Len(t, checkStrangerRun(page), 2)
	})
}

// The committed record of the cold nova-bus run, two names exchanging a
// message on a throwaway Redis, carries its four sections, a real transcript
// and a proposed card for every stumble.
func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(testRoot(t), strangerBusFile))
	require.NoError(t, err, "the record of the run is %s", strangerBusFile)
	assert.Empty(t, checkStrangerRun(string(raw)))
}
