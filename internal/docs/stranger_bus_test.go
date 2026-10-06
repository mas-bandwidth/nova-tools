package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStrangerRunBusTwoNamesIsRecorded holds the cold stranger run of nova-bus
// between two names to its shape: the four sections, a transcript with at
// least one command line, and a proposed card for every stumble.
func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/stranger/bus-two-names.md")
	require.NoError(t, err, "docs/stranger/bus-two-names.md: %v", err)
	text := string(body)

	sections := map[string]string{}
	var current string
	for _, line := range strings.Split(text, "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			current = strings.TrimSpace(name)
			continue
		}
		if current != "" {
			sections[current] += line + "\n"
		}
	}

	for _, heading := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		_, ok := sections[heading]
		assert.True(t, ok, "docs/stranger/bus-two-names.md lacks the section %q", heading)
	}

	command := regexp.MustCompile(`(?m)^\$ \S`)
	assert.Regexp(t, command, sections["Transcript"], "the Transcript section has no command line (a line starting `$ `)")

	cardLine := regexp.MustCompile(`(?m)^card: \S`)
	stumbles := strings.Split(sections["Stumbles"], "\n### ")[1:]
	for _, stumble := range stumbles {
		title, _, _ := strings.Cut(stumble, "\n")
		assert.Regexp(t, cardLine, stumble, "stumble %q has no proposed card line `card: <id>`", title)
	}
}
