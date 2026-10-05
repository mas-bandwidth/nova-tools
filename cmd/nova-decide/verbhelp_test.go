package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads its inputs, asks a backend or writes the record it names
// (the CLI style's rule (b)).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	record := []string{"--record", "{dir}/decisions.jsonl"}
	testverbhelp.Check(t, cli.NoStdin(), []testverbhelp.Case{
		{Verb: "ask", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "read", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "score", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "gate", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "brief", Flags: append([]string{"--backend", "jev", "--card", "{dir}"}, record...)},
		{Verb: "outcome", Flags: record},
		{Verb: "calibrate", Flags: record},
		{Verb: "findings", Flags: record},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli.NoStdin(), "nova-decide", "ask", "read", "score", "gate", "brief", "outcome", "calibrate", "findings", "version")
	// The help a cold reader gets first: what it shows is what the tool does.
	t.Run("schema_and_result_line", helpShowsASchemaAndAResultLine)
	t.Run("result_lines_are_what_ask_prints", helpResultLinesAreWhatAskPrints)
	t.Run("usage_wraps_at_100_columns", helpUsageWrapsAtOneHundredColumns)
	t.Run("examples_run_from_an_empty_dir", usageBannerExamplesRun)
}

// helpShowsASchemaAndAResultLine holds the banner's how-it-works text to
// what a cold reader needs before a first run: the term noul defined where it
// first appears, one inline schema, state and fixed-backend answers file, the
// ANSWER line the run prints, and that exit 0 means the decision was recorded.
func helpShowsASchemaAndAResultLine(t *testing.T) {
	t.Parallel()
	help := cli.OK(t, "help").Stdout

	first := ""
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "noul") {
			first = line
			break
		}
	}
	require.NotEmpty(t, first, "the help never uses the term noul")
	assert.Contains(t, first, "yes-or-no", "noul is not defined where it first appears: %q", first)

	assert.Contains(t, help, `{"name":`, "the help carries no inline schema example")
	assert.Contains(t, help, "ANSWER question=", "the help shows no result line")
	assert.Contains(t, help, "exit 0 means the decision was recorded, never that the answer was yes", "the help does not say exit 0 is recorded, not yes")
}

// The ASK lines the help shows are the lines a recorded ask prints, byte for
// byte: the setup: lines and the first example are run and compared.
func helpResultLinesAreWhatAskPrints(t *testing.T) {
	t.Parallel()
	help := cli.OK(t, "help").Stdout
	examples, err := onboarding.ExampleLines(help, "nova-decide")
	require.NoError(t, err)
	fields, err := onboarding.SplitShell(examples[0])
	require.NoError(t, err)
	res := emptyDir(t, help)(fields[1:])
	require.Equal(t, 0, res.Code, res.Stderr)
	shown := 0
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "ASK ") {
			assert.Contains(t, strings.Split(res.Stdout, "\n"), line, "the help shows a line the ask does not print")
			shown++
		}
	}
	assert.Equal(t, 2, shown, "the help shows the ASK OK line and one ASK ANSWER line")
}

// Every help line is at most 100 columns, and a usage form that wraps continues
// on a line indented deeper than the synopsis it belongs to.
func helpUsageWrapsAtOneHundredColumns(t *testing.T) {
	t.Parallel()
	help := cli.OK(t, "help").Stdout
	_, tail, _ := strings.Cut(help, "usage:\n")
	usage, _, _ := strings.Cut(tail, "\n\n")
	for _, line := range strings.Split(usage, "\n") {
		assert.LessOrEqual(t, len(line), 100, "usage line over 100 columns: %q", line)
		if !strings.HasPrefix(line, "  nova-decide ") {
			assert.True(t, strings.HasPrefix(line, "    "), "a continuation sits at the synopsis column or shallower: %q", line)
		}
	}
}
