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
		{Verb: "hygiene", Flags: []string{"--repo", "{dir}/repo"}},
		{Verb: "dogfood ledger", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "dogfood record", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "dogfood gate", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "dogfood"},
		{Verb: "convergence", Flags: []string{"--state", "{dir}/state"}},
		{Verb: "spelling", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, run, "nova-check", "quickstart", "dogfood gate", "spelling", "version")
	topHelpExplainsItsFailures(t)
}

// topHelpExplainsItsFailures pins the three sentences a cold reader of the one
// page needs and cannot get anywhere else: what an edge is and the next command
// the dogfood gate's exit 1 points at, what a widening tick is and what the
// convergence exit 1 prints and what to run next, and the door to a verb's own
// help (docs/STANDARD.md section 3, "Help is never a refusal"; docs/SPEC.md
// §"dogfood" defines the edge, docs/SPEC-CHECK.md §8 and §11 the stream). The
// banner is run, not read: the words are the ones the tool prints.
func topHelpExplainsItsFailures(t *testing.T) {
	t.Helper()

	exit, help, _ := runCheck(t, "help")
	require.EqualValues(t, 0, exit)
	// (1) The dogfood gate names what an edge is and the next command.
	assert.Contains(t, help, "An edge is a receipt that records a",
		"the dogfood gate's exit-1 line never says what an edge is (docs/SPEC.md §dogfood: an edge is what the run found)")
	assert.Contains(t, help, "finding the run did not clear",
		"the dogfood gate's exit-1 line never says an edge is a finding")
	for _, want := range []string{
		"remedy is one nova-check dogfood record",
		"per verb named, with --ok and a filed",
	} {
		assert.Contains(t, help, want,
			"the dogfood gate's exit-1 line never points at %q", want)
	}
	// (2) Convergence defines a widening tick and says what to run next.
	assert.Contains(t, help, "widening tick is a tick whose",
		"the convergence exit-1 line never defines a widening tick")
	for _, want := range []string{
		"ratio rose against",
		"trend=widening on the CONVERGENCE line",
		"nova-check convergence --state",
	} {
		assert.Contains(t, help, want,
			"the convergence exit-1 sentence never names %q", want)
	}
	// (3) After `nova-check version`, the door to a verb's own help.
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
