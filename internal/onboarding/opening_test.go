package onboarding

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The three readers of a banner's opening (ONBOARDING.md point 6), each with
// the banner that passes and the banners that must not.

func TestOpeningSentenceIsOneSentenceNamingTheTool(t *testing.T) {
	t.Parallel()
	got, err := OpeningSentence("nova-foo: notes between AIs, over a git repository\n\nusage:\n", "nova-foo")
	if err != nil || got != "notes between AIs, over a git repository" {
		t.Fatalf("a good line 1: got %q, %v", got, err)
	}
	for _, tc := range []struct{ name, banner, want string }{
		{"a usage line", "nova-foo check --file <path>\n", `must open with "nova-foo: "`},
		{"another tool's name", "nova-bar: does a thing well\n", `must open with "nova-foo: "`},
		{"a dash for the colon", "nova-foo — owns the thing it owns\n", `must open with "nova-foo: "`},
		{"two words", "nova-foo: the bus\n", "fewer than three words"},
		{"two sentences", "nova-foo: it reads notes. It writes notes too\n", "sentence break"},
		{"a closing full stop", "nova-foo: it reads notes and writes them.\n", "sentence break"},
		{"a pointer in place of the answer", "nova-foo: the ingestion fuse (see docs/SPEC.md)\n", "points at a document"},
		{"flags on line 1", "nova-foo: check --file <path> and report\n", "usage line"},
	} {
		if _, err := OpeningSentence(tc.banner, "nova-foo"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to say %q", tc.name, err, tc.want)
		}
	}
}

func TestHowItWorksOpensNearTheTop(t *testing.T) {
	t.Parallel()
	banner := "nova-foo: does a thing well\n\nhow it works: a box is a file.\nfirst run: init.\n\nusage:\n"
	if got := HowItWorksLine(banner); got != 3 {
		t.Fatalf("HowItWorksLine = %d, want 3", got)
	}
	late := "nova-foo: does a thing well\n\nusage:\n" + strings.Repeat("  nova-foo x\n", 14) + "how it works: too late\n"
	if got := HowItWorksLine(late); got != 0 {
		t.Errorf("a paragraph under the usage lines, past line %d, was found at %d", HowItWorksWithin, got)
	}
	if got := HowItWorksLine("nova-foo: does a thing well\n\nHow it works is below.\n"); got != 0 {
		t.Errorf("prose that mentions the words without the label was found at %d", got)
	}
}

func TestExampleCommandsCountsTheToolsLinesOnly(t *testing.T) {
	t.Parallel()
	banner := "usage:\n  nova-foo run\n\nexample:\n" +
		"  nova-foo check\n" +
		"  mkdir -p /tmp/x/home\n" +
		"  HOME=/tmp/x/home nova-foo probe --write /tmp/x\n" +
		"  nova-foo run --write /tmp/x \\\n" +
		"    -- nova-foo inside\n" +
		"  nova-bar other\n" +
		"\n  nova-foo after the block\n"
	got := ExampleCommands(banner, "nova-foo")
	want := []string{
		"nova-foo check",
		"HOME=/tmp/x/home nova-foo probe --write /tmp/x",
		`nova-foo run --write /tmp/x \`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ExampleCommands =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if got := ExampleCommands("usage:\n  nova-foo run\n", "nova-foo"); len(got) != 0 {
		t.Errorf("a banner with no example: block gave %q", got)
	}
	if got := ExampleCommands("example:\n  FOO-BAR=1 nova-foo x\n  1X=2 nova-foo y\n", "nova-foo"); len(got) != 0 {
		t.Errorf("words that are not assignments were taken as assignments: %q", got)
	}
}

func TestHowItWorksLengthStopsAtTheFirstRunOrABlankLine(t *testing.T) {
	t.Parallel()
	five := "nova-foo: does a thing well\n\nhow it works: one\ntwo\nthree\nfour\nfive\nfirst run: init.\n\nusage:\n"
	if got := HowItWorksLength(five); got != 5 {
		t.Errorf("HowItWorksLength = %d, want 5", got)
	}
	six := "nova-foo: does a thing well\n\nhow it works: one\ntwo\nthree\nfour\nfive\nsix\n\nusage:\n"
	if got := HowItWorksLength(six); got != 6 || got <= HowItWorksMaxLines {
		t.Errorf("HowItWorksLength = %d for a six-line paragraph ending at a blank line, want 6 (over %d)", got, HowItWorksMaxLines)
	}
	if got := HowItWorksLength("nova-foo: does a thing well\n\nusage:\n"); got != 0 {
		t.Errorf("a banner with no paragraph measured %d lines", got)
	}
}

// TestSetupLine reads the setup block up to its blank separator and removes
// banner indentation without changing shell continuation indentation.
func TestSetupLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, banner, want string }{
		{"the line under the heading", "x: y\n\n" + SetupHeading + "\n  mkdir -p ./in && cp a b\n\nexample:\n  x run\n", "mkdir -p ./in && cp a b"},
		{"no heading", "x: y\n\nexample:\n  x run\n", ""},
		{"the heading inside a sentence is no heading", "x: y " + SetupHeading + "\n  mkdir z\n", ""},
		{"multiple steps", "x\n" + SetupHeading + "\n  one\n  two\n\nexample:\n  x run\n", "one\ntwo"},
		{"shell continuation", "x\n" + SetupHeading + "\n  printf '%s' \\\n    '{' \\\n    '}' > ./x\n  printf '\\n' >> ./x\n\nexample:\n  x run\n", "printf '%s' \\\n  '{' \\\n  '}' > ./x\nprintf '\\n' >> ./x"},
		{"whitespace separator", "x\n" + SetupHeading + "\n  one\n  \nexample:\n  x run\n", "one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, SetupLine(tc.banner))
		})
	}
}
