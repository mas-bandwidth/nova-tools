package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// The one verb answers -h and --help with its own help on stdout at exit 0,
// and reads nothing (the CLI style's rule (b), #4505). The scan itself, which takes
// files rather than a verb, has answered -h at exit 0 since before the rule.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "version"},
		{Verb: "reconcile", Flags: []string{"--questions", "{dir}/self-check.md"}},
	})
	testverbhelp.HelpVerb(t, run, "nova-self-talk", "version", "reconcile")
}
