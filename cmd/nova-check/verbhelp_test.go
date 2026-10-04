package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads or writes the place it was pointed at (the CLI style's
// rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "quickstart", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "attest", Flags: []string{"--home", "{dir}/home"}},
		{Verb: "links", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "kernel"},
		{Verb: "nocode"},
		{Verb: "floors"},
		{Verb: "corpus"},
		{Verb: "spelling", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, run, "nova-check", "quickstart", "spelling", "version")
	topHelpExplainsItsFailures(t)
}

// topHelpExplainsItsFailures pins the door to a verb's own help (docs/STANDARD.md
// section 3, "Help is never a refusal"). The banner is run, not read: the words
// are the ones the tool prints.
func topHelpExplainsItsFailures(t *testing.T) {
	t.Helper()

	exit, help, _ := runCheck(t, "help")
	require.EqualValues(t, 0, exit)
	// The door to a verb's own help.
	assert.Contains(t, help, "nova-check <verb> -h, nova-check help <verb>",
		"the top help never names the door to a verb's own help")
	assert.Contains(t, help, "the verb's flags, its effect and exit codes",
		"the top help does not say what the verb's own help prints")
}

// TestTopHelpSetupLinesRunAsPrinted holds the banner's `setup:` block to the
// class rule: its lines are commands, run in order from an empty directory
// before the `example:` lines (internal/onboarding, internal/ci's
// TestPlatformsMatchCILegsAndUnexecutedExamplesOnlyShrink). The mkdir and
// printf that make the tree the quickstart example walks live under it.
func TestTopHelpSetupLinesRunAsPrinted(t *testing.T) {
	t.Parallel()

	_, help, _ := runCheck(t, "help")
	_, tail, found := strings.Cut(help, "\nsetup:\n")
	require.True(t, found, "the banner has no `setup:` heading above `example:`; the two making lines read as prose:\n%s", help)
	setup, example, found := strings.Cut(tail, "\nexample:\n")
	require.True(t, found, "the `setup:` block does not close on `example:`")
	_ = example
	for _, want := range []string{
		"mkdir -p ./self/docs",
		"printf '# Kernel\\n' > ./self/docs/SEED-CORE.md",
	} {
		assert.Contains(t, setup, want, "the `setup:` block is missing %q", want)
	}
}
