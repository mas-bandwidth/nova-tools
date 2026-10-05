package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// strangerSections are the four sections a cold stranger run records, in order.
var strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}

// strangerCardLine is a stumble's proposed card: `card: <id>`.
var strangerCardLine = regexp.MustCompile("(?m)^\\s*(?:[-*]\\s*)?`?card: [a-z0-9][a-z0-9.-]*")

// strangerRunProblems names every way a stranger run record falls short: a
// missing section heading, a transcript with no command line (`$ ` inside a
// fence), or a stumble (a `###` heading under Stumbles) with no `card: <id>`.
func strangerRunProblems(doc string) []string {
	var problems []string
	sections := map[string]string{}
	var current string
	var body strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.Split(doc, "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			current = strings.TrimSpace(name)
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()

	for _, name := range strangerSections {
		if _, ok := sections[name]; !ok {
			problems = append(problems, "no section ## "+name)
		}
	}
	if transcript, ok := sections["Transcript"]; ok && !hasFencedCommand(transcript) {
		problems = append(problems, "the transcript has no command line ($ inside a fence)")
	}
	if stumbles, ok := sections["Stumbles"]; ok {
		parts := strings.Split(stumbles, "\n### ")
		if !strings.HasPrefix(stumbles, "### ") {
			parts = parts[1:]
		}
		for _, part := range parts {
			title, rest, _ := strings.Cut(strings.TrimPrefix(part, "### "), "\n")
			if !strangerCardLine.MatchString(rest) {
				problems = append(problems, "stumble "+strings.TrimSpace(title)+" has no card: <id> line")
			}
		}
	}
	return problems
}

// hasFencedCommand reports whether a fenced block holds a line opening `$ `.
func hasFencedCommand(text string) bool {
	in := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			in = !in
			continue
		}
		if in && strings.HasPrefix(line, "$ ") {
			return true
		}
	}
	return false
}

// TestStrangerRunThreeCardSprintIsRecorded holds docs/stranger/three-card-sprint.md
// to the record a cold stranger run owes: Setup, Transcript, Stumbles and
// Verdict, a transcript of real command lines, and a proposed card for every
// stumble, which the coordinator turns into work.
func TestStrangerRunThreeCardSprintIsRecorded(t *testing.T) {
	t.Parallel()

	good := "## Setup\nbench\n## Transcript\n```\n$ nova-sprint version\nnova-sprint 1.0.0\n```\n" +
		"## Stumbles\n### one\nread x\ncard: fix-one\n### two\n- card: fix-two\n## Verdict\nyes\n"
	rows := []struct {
		name string
		doc  string
		want string
	}{
		{"complete", good, ""},
		{"no setup", strings.Replace(good, "## Setup\n", "", 1), "no section ## Setup"},
		{"no transcript", strings.Replace(good, "## Transcript\n", "## Log\n", 1), "no section ## Transcript"},
		{"no stumbles", strings.Replace(good, "## Stumbles\n", "", 1), "no section ## Stumbles"},
		{"no verdict", strings.Replace(good, "## Verdict\n", "", 1), "no section ## Verdict"},
		{"no command", strings.Replace(good, "$ nova-sprint", "nova-sprint", 1), "no command line"},
		{"command outside fence", strings.Replace(good, "```\n$ nova-sprint version\n", "$ nova-sprint version\n```\n", 1), "no command line"},
		{"stumble without card", strings.Replace(good, "- card: fix-two\n", "proposed: fix it\n", 1), "stumble two has no card"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(strangerRunProblems(row.doc), "; ")
			if row.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, row.want)
		})
	}

	raw, err := os.ReadFile("../../docs/stranger/three-card-sprint.md")
	require.NoError(t, err, "docs/stranger/three-card-sprint.md: %v", err)
	assert.Empty(t, strangerRunProblems(string(raw)), "docs/stranger/three-card-sprint.md")
}
