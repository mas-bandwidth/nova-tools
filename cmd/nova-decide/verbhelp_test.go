package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
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
		{Verb: "brief", Flags: append([]string{"--backend", "jev", "--card", "{dir}"}, record...)},
		{Verb: "outcome", Flags: record},
		{Verb: "calibrate", Flags: record},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli.NoStdin(), "nova-decide", "ask", "read", "brief", "outcome", "calibrate", "version")
}
