package main

import (
	"io"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads a transcript or writes a day file (the CLI style's rule
// (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	out := []string{"--out", "{dir}/out"}
	testverbhelp.Check(t, tokensRun, []testverbhelp.Case{
		{Verb: "fold", Flags: out},
		{Verb: "collate", Flags: out},
		{Verb: "report"},
		{Verb: "ledger"},
		{Verb: "sum"},
		{Verb: "check"},
		{Verb: "sources"},
		{Verb: "profiles"},
		{Verb: "session"},
		{Verb: "fold-pool"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, tokensRun, "nova-tokens", "fold", "sources", "version")
}

func tokensRun(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, time.Now().UTC())
}
