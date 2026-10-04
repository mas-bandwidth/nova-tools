package onboarding

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three readers of a banner's opening (ONBOARDING.md point 6), each with
// the banner that passes and the banners that must not. The banners are stood
// by the rig in rig_test.go; the checks that repeat live there too.

func TestOpeningSentenceIsOneSentenceNamingTheTool(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
		good  bool
	}{
		{"valid opening", []string{"nova-foo: notes between AIs, over a git repository", "", "usage:"}, "notes between AIs, over a git repository", true},
		{"a usage line", []string{"nova-foo check --file <path>"}, `must open with "nova-foo: "`, false},
		{"another tool's name", []string{"nova-bar: does a thing well"}, `must open with "nova-foo: "`, false},
		{"a dash for the colon", []string{"nova-foo — owns the thing it owns"}, `must open with "nova-foo: "`, false},
		{"two words", []string{"nova-foo: the bus"}, "fewer than three words", false},
		{"two sentences", []string{"nova-foo: it reads notes. It writes notes too"}, "sentence break", false},
		{"a closing full stop", []string{"nova-foo: it reads notes and writes them."}, "sentence break", false},
		{"a pointer in place of the answer", []string{"nova-foo: the ingestion fuse (see docs/SPEC.md)"}, "points at a document", false},
		{"flags on line 1", []string{"nova-foo: check --file <path> and report"}, "usage line", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := OpeningSentence(r.banner(tc.lines...), r.tool)
			if tc.good {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			} else {
				assert.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestHowItWorksOpensNearTheTop(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, tc := range []struct {
		name string
		line []string
		want int
	}{
		{"early position", []string{"nova-foo: does a thing well", "", "how it works: a box is a file.", "first run: init.", "", "usage:"}, 3},
		{"after usage", []string{"nova-foo: does a thing well", "", "usage:", strings.Repeat("  nova-foo x\n", 14) + "how it works: too late"}, 0},
		{"no how it works", []string{"nova-foo: does a thing well", "", "How it works is below."}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HowItWorksLine(r.banner(tc.line...))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExampleCommandsCountsTheToolsLinesOnly(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, tc := range []struct {
		name string
		text []string
		want []string
	}{
		{
			"extracts tool lines from example block",
			[]string{
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
			},
			[]string{
				"nova-foo check",
				"HOME=/tmp/x/home nova-foo probe --write /tmp/x",
				`nova-foo run --write /tmp/x \`,
			},
		},
		{"no example block", []string{"usage:", "  nova-foo run"}, nil},
		{"no tool lines", []string{"example:", "  FOO-BAR=1 nova-foo x", "  1X=2 nova-foo y"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r.commands(r.banner(tc.text...), tc.want...)
		})
	}
}

func TestHowItWorksLengthStopsAtTheFirstRunOrABlankLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, tc := range []struct {
		name string
		line []string
		want int
	}{
		{"stops at first run", []string{"nova-foo: does a thing well", "", "how it works: one", "two", "three", "four", "five", "first run: init.", "", "usage:"}, 5},
		{"stops at blank line", []string{"nova-foo: does a thing well", "", "how it works: one", "two", "three", "four", "five", "six", "", "usage:"}, 6},
		{"no how it works", []string{"nova-foo: does a thing well", "", "usage:"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HowItWorksLength(r.banner(tc.line...))
			assert.Equal(t, tc.want, got)
		})
	}
}
