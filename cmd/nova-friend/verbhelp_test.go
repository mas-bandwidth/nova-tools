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
		{Verb: "run", Flags: store},
		{Verb: "install", Flags: store},
		{Verb: "uninstall"},
		{Verb: "ping", Flags: store},
		{Verb: "pong", Flags: store},
		{Verb: "wait-pong", Flags: store},
		{Verb: "status"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli, "nova-friend", "run", "install", "uninstall", "ping", "pong", "wait-pong", "status", "version")
}
