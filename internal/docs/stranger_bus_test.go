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

// A stranger run is a recorded cold run of one tool by someone new to it. The
// record is only useful to the coordinator if it has all four sections, a
// transcript that shows real commands, and a proposed card for every stumble.
func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(testRoot(t), "docs", "stranger", "bus-two-names.md"))
	require.NoError(t, err, "the stranger run is not recorded")
	page := string(raw)

	sections := map[string]string{}
	var name string
	for _, line := range strings.Split(page, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			name = strings.TrimSpace(h)
			continue
		}
		if name != "" {
			sections[name] += line + "\n"
		}
	}
	for _, want := range []string{"Setup", "Transcript", "Stumbles", "Verdict"} {
		assert.NotEmpty(t, strings.TrimSpace(sections[want]), "section %q is missing or empty", want)
	}

	assert.Regexp(t, regexp.MustCompile(`(?m)^\$ nova-bus send `), sections["Transcript"], "the transcript has no nova-bus send command line")
	assert.Regexp(t, regexp.MustCompile(`(?m)^\$ nova-bus ack `), sections["Transcript"], "the transcript has no nova-bus ack command line")

	// Every stumble is a "### " entry under Stumbles and carries `card: <id>`.
	card := regexp.MustCompile("(?m)^(- )?card: `?[a-z0-9][a-z0-9-]*`?")
	for _, entry := range strings.Split("\n"+sections["Stumbles"], "\n### ")[1:] {
		title, _, _ := strings.Cut(entry, "\n")
		assert.Regexp(t, card, entry, "stumble %q has no proposed card line `card: <id>`", title)
	}
	assert.Contains(t, sections["Stumbles"], "### ", "the Stumbles section lists no stumble")
	assert.Regexp(t, `(?i)could a stranger do it: (yes|no)`, sections["Verdict"], "the verdict does not answer yes or no")
}
