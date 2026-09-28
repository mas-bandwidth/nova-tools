package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them opens the store it was pointed at (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	store := []string{"--store", "{dir}/cairns", "--session", "s1"}
	testverbhelp.Check(t, cairnRun, []testverbhelp.Case{
		{Verb: "open", Flags: store},
		{Verb: "append", Flags: store},
		{Verb: "index", Flags: store},
		{Verb: "receipt", Flags: store},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cairnRun, "nova-cairn", "open", "append", "version")
}

func cairnRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr)
}
