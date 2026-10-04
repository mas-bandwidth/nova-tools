package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// reads and writes nothing (the CLI style's rule (b), #4505); `example` is
// handed a directory first, and the check holds that nothing was made there.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "version"},
		{Verb: "scan"},
		{Verb: "shapes"},
		{Verb: "example", Flags: []string{"--dry-run"}},
	})
}
