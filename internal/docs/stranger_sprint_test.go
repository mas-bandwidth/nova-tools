package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// strangerSections are the four sections a cold stranger run records.
var strangerSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}

// strangerCommandLine is a transcript line a stranger typed: a dollar prompt
// and a command.
var strangerCommandLine = regexp.MustCompile(`(?m)^\$ \S`)

// strangerCardLine is a stumble's proposed card, a line `card: <id>`.
var strangerCardLine = regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9.-]*[ \t]*$`)

// strangerRunProblems names every way a stranger-run record falls short: a
// missing section heading, a transcript with no command line, or a stumble
// with no proposed card line.
func strangerRunProblems(doc string) []string {
	doc = strings.ReplaceAll(doc, "\r\n", "\n")
	sections := map[string]string{}
	var name string
	var b strings.Builder
	flush := func() {
		if name == "" {
			return
		}
		sections[name] = b.String()
		b.Reset()
	}
	for _, line := range strings.Split(doc, "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok && !strings.HasPrefix(rest, "#") {
			flush()
			name = strings.TrimSpace(rest)
			continue
		}
		if name != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	flush()

	var problems []string
	for _, want := range strangerSections {
		if _, ok := sections[want]; !ok {
			problems = append(problems, "missing section heading "+want)
		}
	}
	if transcript, ok := sections["Transcript"]; ok && !strangerCommandLine.MatchString(transcript) {
		problems = append(problems, "the transcript has no command line")
	}
	if stumbles, ok := sections["Stumbles"]; ok {
		var block strings.Builder
		var n int
		flushBlock := func() {
			if block.Len() == 0 {
				return
			}
			n++
			if !strangerCardLine.MatchString(block.String()) {
				problems = append(problems, "a stumble has no line `card: <id>`")
			}
			block.Reset()
		}
		for _, line := range strings.Split(stumbles, "\n") {
			if strings.HasPrefix(line, "### ") {
				flushBlock()
			}
			if strings.HasPrefix(line, "### ") || block.Len() > 0 {
				block.WriteString(line)
				block.WriteByte('\n')
			}
		}
		flushBlock()
	}
	return problems
}

// TestStrangerRunThreeCardSprintIsRecorded holds
// docs/stranger/three-card-sprint.md to the record a cold stranger run owes.
// It is red while one of the four section headings is missing, the transcript
// has no command line, or a stumble has no proposed card line.
func TestStrangerRunThreeCardSprintIsRecorded(t *testing.T) {
	t.Parallel()

	good := "## Setup\nbench\n## Transcript\n\n$ nova-sprint version\nnova-sprint 1\n\n## Stumbles\n\n### one\n\nread the help\n\ncard: fix-one\n\n## Verdict\n\ncould a stranger do it: no\n"
	rows := []struct {
		name string
		doc  string
		want string
	}{
		{"complete", good, ""},
		{"no setup", strings.Replace(good, "## Setup\n", "", 1), "missing section heading Setup"},
		{"no transcript", strings.Replace(good, "## Transcript\n", "## Log\n", 1), "missing section heading Transcript"},
		{"no stumbles", strings.Replace(good, "## Stumbles\n", "", 1), "missing section heading Stumbles"},
		{"no verdict", strings.Replace(good, "## Verdict\n", "", 1), "missing section heading Verdict"},
		{"no command", strings.Replace(good, "$ nova-sprint version", "nova-sprint version", 1), "no command line"},
		{"stumble without card", strings.Replace(good, "card: fix-one\n", "proposed: fix it\n", 1), "card: <id>"},
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
