package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

func runHelp(args []string, stdout, stderr io.Writer) int {
	return run(args, &bytes.Buffer{}, stdout, stderr)
}

// Every verb answers -h and --help with its own help on stdout at exit 0,
// reading nothing: the corpus flags point at a directory that must stay
// empty.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, runHelp, []testverbhelp.Case{
		{Verb: "screen", Flags: []string{"--root", "{dir}"}},
		{Verb: "corpus", Flags: []string{"--root", "{dir}"}},
		{Verb: "version"},
	})
}

func TestHelpVerbMatchesTheFlag(t *testing.T) {
	t.Parallel()
	testverbhelp.HelpVerb(t, runHelp, "nova-privacy", "screen", "corpus")
}
