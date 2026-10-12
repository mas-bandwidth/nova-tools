package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them dials the store it names (the CLI style's rule (b)).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	store := []string{"--redis", "{addr}"}
	cli := newRig(t).cli().NoStdin()
	testverbhelp.Check(t, cli, []testverbhelp.Case{
		{Verb: "hook", Flags: store},
		{Verb: "prove", Flags: store},
		{Verb: "unhook", Flags: store},
		{Verb: "status", Flags: store},
	})
	testverbhelp.HelpVerb(t, cli, "nova-hook", "hook", "prove", "unhook", "status")
}
