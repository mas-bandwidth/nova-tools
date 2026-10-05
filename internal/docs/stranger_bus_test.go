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

// The stranger run of nova-bus between two names is a recorded cold run: the
// file carries its four sections, a transcript with at least one command line,
// and a proposed card for every stumble, so the coordinator can turn each
// stumble into a card without asking the stranger again.
const strangerBusDoc = "docs/stranger/bus-two-names.md"

var strangerCardLine = regexp.MustCompile("(?m)^card: [a-z0-9][a-z0-9-]*\\s")

func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(testRoot(t), strangerBusDoc))
	require.NoError(t, err, "the recorded run %s must exist", strangerBusDoc)
	sections := strangerSections(string(raw))

	for _, heading := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		body, ok := sections[heading]
		require.True(t, ok, "%s has no %q section", strangerBusDoc, heading)
		assert.NotEmpty(t, strings.TrimSpace(body), "the %q section is empty", heading)
	}

	assert.Regexp(t, `(?m)^\$ \S`, sections["Transcript"], "the transcript has no command line (a line starting with \"$ \")")
	assert.Contains(t, sections["Transcript"], "nova-bus send", "the transcript shows no send")
	assert.Contains(t, sections["Transcript"], "nova-bus ack", "the transcript shows no ack")
	assert.Contains(t, sections["Setup"], "nova-bus", "Setup prints no nova-bus version")

	for _, stumble := range strings.Split(sections["Stumbles"], "\n### ")[1:] {
		title, _, _ := strings.Cut(stumble, "\n")
		assert.Regexp(t, strangerCardLine, stumble+"\n", "stumble %q has no proposed card line `card: <id>`", title)
	}

	assert.Regexp(t, `(?mi)^could a stranger do it: (yes|no)\b`, sections["Verdict"], "the verdict does not say yes or no")
	assert.Regexp(t, `(?mi)^minutes: \d+`, sections["Verdict"], "the verdict does not give minutes taken")
}

// strangerSections splits a page into its "## " sections by heading.
func strangerSections(page string) map[string]string {
	out := map[string]string{}
	parts := strings.Split("\n"+page, "\n## ")
	for _, part := range parts[1:] {
		heading, body, _ := strings.Cut(part, "\n")
		out[strings.TrimSpace(heading)] = body
	}
	return out
}
