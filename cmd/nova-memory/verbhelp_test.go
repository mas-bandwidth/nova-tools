package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them indexes or writes the root it was pointed at
// (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	root := []string{"--root", "{dir}"}
	testverbhelp.Check(t, memoryRun, []testverbhelp.Case{
		{Verb: "quickstart", Flags: append(root, "--draft", "{dir}/draft.md")},
		{Verb: "stats", Flags: root},
		{Verb: "search", Flags: root},
		{Verb: "check", Flags: root},
		{Verb: "verify", Flags: root},
		{Verb: "eval", Flags: root},
		{Verb: "boot", Flags: root},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, memoryRun, "nova-memory", "search", "version")
}

func memoryRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr)
}
