package main

import (
	"testing"

	"github.com/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them dials the store it names (the CLI style's rule (b)).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	store := []string{"--redis", "{addr}"}
	cli := newRig().cli().NoStdin()
	testverbhelp.Check(t, cli, []testverbhelp.Case{
		{Verb: "wait", Flags: store},
		{Verb: "send", Flags: store},
		{Verb: "recv", Flags: store},
		{Verb: "ack", Flags: store},
		{Verb: "peek", Flags: store},
		{Verb: "receipts", Flags: store},
		{Verb: "overdue", Flags: store},
		{Verb: "log", Flags: store},
		{Verb: "names", Flags: store},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli, "nova-bus", "wait", "send", "peek", "recv", "ack", "receipts", "overdue", "log", "names", "version")
}
