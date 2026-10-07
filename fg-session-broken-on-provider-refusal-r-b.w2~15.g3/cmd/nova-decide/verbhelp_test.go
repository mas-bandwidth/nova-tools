package main

import (
	"strings"
	"testing"

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
}

// TestHelpShowsASchemaAndAResultLine holds the banner's how-it-works text to
// what a cold reader needs before a first run: the term noul defined where it
// first appears, one inline schema, state and fixed-backend answers file, the
// ANSWER line the run prints, and that exit 0 means the decision was recorded.
func TestHelpShowsASchemaAndAResultLine(t *testing.T) {
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
	assert.Contains(t, help, "exit 0 means recorded, never approved", "the help does not say exit 0 is recorded, not approved")
}
