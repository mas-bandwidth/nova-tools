package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// strangerBusFile is the recorded cold run of nova-bus between two names.
const strangerBusFile = "../../docs/stranger/bus-two-names.md"

var strangerCardLine = regexp.MustCompile(`(?m)^card: [a-z0-9][a-z0-9.-]*$`)

// strangerSections splits a document on its "## " headings: heading text to body.
func strangerSections(doc string) map[string]string {
	out := map[string]string{}
	var name string
	for _, line := range strings.Split(doc, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			name = strings.TrimSpace(h)
			out[name] = ""
			continue
		}
		if name != "" {
			out[name] += line + "\n"
		}
	}
	return out
}

// TestStrangerRunBusTwoNamesIsRecorded holds the stranger run of nova-bus to its
// shape: the four sections, a transcript with at least one command line, and a
// proposed card line under every stumble.
func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(strangerBusFile)
	require.NoError(t, err, "the run is recorded in docs/stranger/bus-two-names.md")
	sections := strangerSections(string(raw))

	for _, name := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		assert.Contains(t, sections, name, "docs/stranger/bus-two-names.md lacks the %q section", name)
	}

	assert.Regexp(t, `(?m)^\$ \S`, sections["Transcript"], "the Transcript section has no command line (`$ <command>`)")

	var stumbles int
	for _, block := range strings.Split(sections["Stumbles"], "\n### ")[1:] {
		stumbles++
		title, _, _ := strings.Cut(block, "\n")
		assert.Regexp(t, strangerCardLine, block, "stumble %q has no proposed card line `card: <id>`", title)
	}
	assert.NotZero(t, stumbles, "the Stumbles section holds no stumble (### heading); a run with none says so in the Verdict")

	assert.Regexp(t, `(?mi)^could a stranger do it: (yes|no)\b`, sections["Verdict"], "the Verdict lacks `could a stranger do it: yes|no`")
	assert.Regexp(t, `(?mi)^minutes: \d+`, sections["Verdict"], "the Verdict lacks `minutes: <n>`")
}
