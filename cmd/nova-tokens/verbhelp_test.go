package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads a transcript or writes a day file (the CLI style's rule
// (b), #4505).
//
// The same run holds the three banner lines a cold rating named as the costliest
// (docs/STANDARD.md section 3, ONBOARDING point 6: the banner answers what the
// tool does, how it works and how a reader uses it): profiles, the one usage line
// that carried no sentence, states what it prints; report names each of its two
// modes in its own usage line's first clause, with one sentence saying which flags
// select which mode and what a mix of them prints; and the fixture setup is a block
// of short lines under `setup:` rather than one line no reader can parse.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	out := []string{"--out", "{dir}/out"}
	testverbhelp.Check(t, tokensRun, []testverbhelp.Case{
		{Verb: "fold", Flags: out},
		{Verb: "report"},
		{Verb: "ledger"},
		{Verb: "sum"},
		{Verb: "check"},
		{Verb: "sources"},
		{Verb: "profiles"},
		{Verb: "session"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, tokensRun, "nova-tokens", "fold", "sources", "version")

	var banner bytes.Buffer
	require.Equal(t, 0, tokensRun([]string{"help"}, &banner, io.Discard), "`help` exits non-zero")
	text := banner.String()
	lines := strings.Split(text, "\n")

	// profiles: the sentence under the usage line says what a run prints -- the line
	// prefixes and fields it names are the ones
	// TestSwarmProfilesMeasureExplicitReasoningEffortAndBudgetOvershootOnRealWork runs.
	profilesAt := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "nova-tokens profiles --swarm-root <dir>" {
			profilesAt = i
		}
	}
	require.GreaterOrEqual(t, profilesAt, 0, "the usage block carries no `nova-tokens profiles --swarm-root <dir>` line:\n%s", text)
	require.Less(t, profilesAt+1, len(lines), "the profiles usage line is the banner's last")
	assert.Contains(t, lines[profilesAt+1], "one PROFILES MODEL line per model",
		"the profiles usage line carries no sentence saying what it prints:\n%s", text)
	assert.Contains(t, lines[profilesAt+1], "PROFILES OK",
		"the profiles sentence does not name the closing line:\n%s", text)

	// report: each mode named in its own usage line's first clause, and one sentence
	// for which flags select which -- a mix of --who and --redis was run, and --redis
	// won, so the sentence says so.
	for _, want := range []string{
		"  nova-tokens report (local mode) --who <name> --day <YYYY-MM-DD> --repos <file>",
		"  nova-tokens report (store mode) --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]",
		"  the local mode is selected by --who and --day; the store mode by --redis and --month; giving both --who and --redis selects the store mode (--redis wins); a mix of --who and --redis prints the store summary",
	} {
		assert.Contains(t, text, "\n"+want+"\n", "the usage block does not carry this line as printed:\n%s", want)
	}

	// setup: a block of lines, each under 100 columns as printed (the banner indents
	// two), that TestHelpExampleLinesRunAsPrinted runs in order before the block.
	setup := fixtureSetupLines(text)
	require.NotEmpty(t, setup, "the banner carries no `setup:` block above its `example:` block:\n%s", text)
	assert.Equal(t, wantFixtureSetup, setup[0], "the setup block does not open with the line its reader needs first")
	for _, line := range setup {
		assert.Less(t, len(line)+2, 100, "the setup line is %d columns as printed, want under 100:\n  %s", len(line)+2, line)
	}
}

func tokensRun(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, time.Now().UTC())
}
