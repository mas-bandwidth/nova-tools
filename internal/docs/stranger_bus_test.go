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

// stranger_bus_test.go holds the stranger lens's first record: a cold run of
// nova-bus by a bud who has only the README and the tools' help, two names
// sending each other a message on a throwaway Redis. The record is
// docs/stranger/bus-two-names.md. It carries four sections (Setup, Transcript,
// Stumbles, Verdict); the transcript has at least one command line (a line
// that starts with "$ "); and every stumble, a "### " heading under Stumbles,
// carries a proposed card line "card: <id>", because the coordinator turns each
// stumble into a card and a stumble with no card is a finding that goes nowhere.

const strangerBusPath = "../../docs/stranger/bus-two-names.md"

var strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}

var strangerCardLine = regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9-]*[a-z0-9]\b`)

// strangerRecordProblems names every way the record falls short, all at once.
func strangerRecordProblems(md string) []string {
	var problems []string
	bodies := map[string]string{}
	var cur string
	for _, line := range strings.Split(md, "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			cur = strings.TrimSpace(name)
			if _, dup := bodies[cur]; !dup {
				bodies[cur] = ""
			}
			continue
		}
		if cur != "" {
			bodies[cur] += line + "\n"
		}
	}
	for _, s := range strangerSections {
		if _, ok := bodies[s]; !ok {
			problems = append(problems, "missing the section heading \"## "+s+"\"")
		}
	}
	if body, ok := bodies["Transcript"]; ok && !strings.Contains("\n"+body, "\n$ ") {
		problems = append(problems, "the Transcript has no command line (a line starting \"$ \")")
	}
	if body, ok := bodies["Stumbles"]; ok {
		for _, part := range strings.Split("\n"+body, "\n### ")[1:] {
			title, rest, _ := strings.Cut(part, "\n")
			if !strangerCardLine.MatchString(rest) {
				problems = append(problems, "the stumble \""+strings.TrimSpace(title)+"\" has no proposed card line \"card: <id>\"")
			}
		}
	}
	return problems
}

const strangerGood = "## Setup\nbench\n## Transcript\n```\n$ nova-bus version\nok\n```\n## Stumbles\n### one\nread x\ncard: fix-one\n## Verdict\nyes\n"

func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()

	md, err := os.ReadFile(filepath.FromSlash(strangerBusPath))
	require.NoError(t, err, "docs/stranger/bus-two-names.md is the record of the cold run: write it")
	assert.Empty(t, strangerRecordProblems(string(md)), "docs/stranger/bus-two-names.md")
}

func TestStrangerRecordCheckRefusesWhatItShould(t *testing.T) {
	t.Parallel()

	require.Empty(t, strangerRecordProblems(strangerGood))
	for name, tc := range map[string]struct {
		md   string
		want string
	}{
		"no setup":        {strings.Replace(strangerGood, "## Setup\n", "", 1), `"## Setup"`},
		"no transcript":   {strings.Replace(strangerGood, "## Transcript\n", "", 1), `"## Transcript"`},
		"no stumbles":     {strings.Replace(strangerGood, "## Stumbles\n", "", 1), `"## Stumbles"`},
		"no verdict":      {strings.Replace(strangerGood, "## Verdict\n", "", 1), `"## Verdict"`},
		"no command line": {strings.Replace(strangerGood, "$ nova-bus version", "nova-bus version", 1), "no command line"},
		"stumble no card": {strings.Replace(strangerGood, "card: fix-one\n", "", 1), `"one" has no proposed card`},
		"card in another stumble does not count": {
			strings.Replace(strangerGood, "card: fix-one\n", "", 1) + "### two\ncard: fix-two\n", `"one" has no proposed card`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, strings.Join(strangerRecordProblems(tc.md), "\n"), tc.want)
		})
	}
}
