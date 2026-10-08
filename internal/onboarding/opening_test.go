package onboarding

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpeningSentenceIsOneSentenceNamingTheTool(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	got, err := OpeningSentence(r.banner("nova-foo: notes between AIs, over a git repository", "", "usage:"), r.tool)
	require.NoError(t, err, "a good line 1: got %q, %v", got, err)
	require.Equal(t, "notes between AIs, over a git repository", got, "a good line 1: got %q, %v", got, err)
	for _, tc := range []struct{ name, banner, want string }{
		{"a usage line", r.banner("nova-foo check --file <path>"), `must open with "nova-foo: "`},
		{"another tool's name", r.banner("nova-bar: does a thing well"), `must open with "nova-foo: "`},
		{"a dash for the colon", r.banner("nova-foo — owns the thing it owns"), `must open with "nova-foo: "`},
		{"two words", r.banner("nova-foo: the bus"), "fewer than three words"},
		{"two sentences", r.banner("nova-foo: it reads notes. It writes notes too"), "sentence break"},
		{"a closing full stop", r.banner("nova-foo: it reads notes and writes them."), "sentence break"},
		{"a pointer in place of the answer", r.banner("nova-foo: the ingestion fuse (see docs/SPEC.md)"), "points at a document"},
		{"flags on line 1", r.banner("nova-foo: check --file <path> and report"), "usage line"},
	} {
		r.refuses(tc.name, tc.banner, tc.want)
	}
}

func TestHowItWorksOpensNearTheTop(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.Equal(t, 3, HowItWorksLine(r.banner("nova-foo: does a thing well", "", "how it works: a box is a file.", "first run: init.", "", "usage:")))
	r.foundAt(r.banner("nova-foo: does a thing well", "", "usage:", strings.Repeat("  nova-foo x\n", 14)+"how it works: too late"), 0)
	r.foundAt(r.banner("nova-foo: does a thing well", "", "How it works is below."), 0)
}

func TestExampleCommandsCountsTheToolsLinesOnly(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.commands(r.banner(
		"usage:",
		"  nova-foo run",
		"",
		"example:",
		"  nova-foo check",
		"  mkdir -p /tmp/x/home",
		"  HOME=/tmp/x/home nova-foo probe --write /tmp/x",
		"  nova-foo run --write /tmp/x \\",
		"    -- nova-foo inside",
		"  nova-bar other",
		"",
		"  nova-foo after the block",
	),
		"nova-foo check",
		"HOME=/tmp/x/home nova-foo probe --write /tmp/x",
		`nova-foo run --write /tmp/x \`,
	)
	r.commands(r.banner("usage:", "  nova-foo run"))
	r.commands(r.banner("example:", "  FOO-BAR=1 nova-foo x", "  1X=2 nova-foo y"))
}

func TestHowItWorksLengthStopsAtTheFirstRunOrABlankLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.counted(r.banner("nova-foo: does a thing well", "", "how it works: one", "two", "three", "four", "five", "first run: init.", "", "usage:"), 5)
	got := HowItWorksLength(r.banner("nova-foo: does a thing well", "", "how it works: one", "two", "three", "four", "five", "six", "", "usage:"))
	assert.True(t, got == 6 && got > HowItWorksMaxLines, "HowItWorksLength = %d for a six-line paragraph ending at a blank line, want 6 (over %d)", got, HowItWorksMaxLines)
	r.counted(r.banner("nova-foo: does a thing well", "", "usage:"), 0)
}
