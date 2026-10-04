package main

import (
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads a checkout, runs a child, dials a forge or writes
// anything (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, releaseRun, []testverbhelp.Case{
		{Verb: "cut"},
		{Verb: "build"},
		{Verb: "install"},
		{Verb: "adopt"},
		{Verb: "pull"},
		{Verb: "cycle"},
		{Verb: "version"},
	})
}

func releaseRun(args []string, stdout, stderr io.Writer) int {
	return release.Main("nova-release", args, "", stdout, stderr)
}
