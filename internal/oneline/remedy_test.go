package oneline

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithRemedyEndsEveryLineWithANextStep pins the two halves: a line with no
// remedy gains "; run: <next>", and a line that names one already is left
// exactly as it was.
func TestWithRemedyEndsEveryLineWithANextStep(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ what, want string }{
		{"the harness binary /x is missing", "the harness binary /x is missing; run: tool verb -h"},
		{"--out exists; pass --overwrite to replace it", "--out exists; pass --overwrite to replace it"},
		{"no store; run: tool init", "no store; run: tool init"},
		{"LINT FAIL remedy=tool lint --max 0", "LINT FAIL remedy=tool lint --max 0"},
		{"--max must be 1 to 64; give 1 to 64", "--max must be 1 to 64; give 1 to 64"},
	} {
		assert.Equal(t, c.want, WithRemedy(c.what, "tool verb -h"), "WithRemedy(%q)", c.what)
	}
	assert.Equal(t, "two\\x0alines; run: tool -h", WithRemedy("two\nlines", "tool -h"), "WithRemedy escapes")
	once := WithRemedy(Err(errString("a\tb")), "tool -h")
	assert.Equal(t, once, Escape(once), "WithRemedy(Err(...)) is not stable under Escape")
	for _, s := range []string{"the store refused the write", "cannot read the file", "the name of the card"} {
		assert.False(t, HasRemedy(s), "HasRemedy(%q) = true; the line names no next step", s)
	}
}

// TestHasRemedyIsNotFooledByProse pins the loose words that used to count: "see",
// "want", "retry" and "help" inside a sentence are prose, and a line holding only
// them names no next step. The forms that do name one are still read.
func TestHasRemedyIsNotFooledByProse(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"the card is not ready, see above",
		"you may want to look at it",
		"the store is busy, retry later",
		"no help is available for the card",
		"the lease is held; want it? see it",
	} {
		assert.False(t, HasRemedy(s), "HasRemedy(%q) = true; the line is prose", s)
	}
	for _, s := range []string{
		"the card is not ready; run: nova-sprint cards",
		"see nova-sprint help claim",
		"nova-swarm help batch",
		"retry with --force",
		"see --help",
		"the verb wants --sprint",
		"see `nova-sprint cards`",
	} {
		assert.True(t, HasRemedy(s), "HasRemedy(%q) = false; the line names its next step", s)
	}
}

// TestShellWordReadsBackAsOneWord: a value through ShellWord, split as a POSIX
// shell splits it (onboarding.SplitShell, nothing executed), is that one value;
// a value with nothing a shell reads is printed as it is.
func TestShellWordReadsBackAsOneWord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ value, printed string }{
		{"./tree.lisp", "./tree.lisp"},
		{"/usr/local/bin/gh", "/usr/local/bin/gh"},
		{"-x", "-x"},
		{"", "''"},
		{"work trees", "'work trees'"},
		{"it's", `'it'"'"'s'`},
		{`say "hi"`, `'say "hi"'`},
		{"$(x)", "'$(x)'"},
		{"a;b", "'a;b'"},
		{"`id`", "'`id`'"},
		{"line\nbreak", "'line\nbreak'"},
		{`back\slash`, `'back\slash'`},
		{"*", "'*'"},
	} {
		t.Run(tc.printed, func(t *testing.T) {
			t.Parallel()
			got := ShellWord(tc.value)
			assert.Equal(t, tc.printed, got)
			if strings.Contains(tc.value, "`") {
				return // the splitter refuses a backquote anywhere; the quoting is pinned above
			}
			words, err := onboarding.SplitShell("cmd " + got + " tail")
			require.NoError(t, err)
			assert.Equal(t, []string{"cmd", tc.value, "tail"}, words)
		})
	}
}
