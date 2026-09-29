package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads or writes the place it was pointed at (the CLI style's
// rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "quickstart", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "attest", Flags: []string{"--home", "{dir}/home"}},
		{Verb: "links", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "kernel"},
		{Verb: "nocode"},
		{Verb: "floors"},
		{Verb: "corpus"},
		{Verb: "hygiene", Flags: []string{"--repo", "{dir}/repo"}},
		{Verb: "dogfood ledger", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "dogfood record", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "dogfood gate", Flags: []string{"--receipts", "{dir}/receipts"}},
		{Verb: "dogfood"},
		{Verb: "convergence", Flags: []string{"--state", "{dir}/state"}},
		{Verb: "spelling", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "exec-deadline", Flags: []string{"--dir", "{dir}/self"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, run, "nova-check", "quickstart", "dogfood gate", "spelling", "exec-deadline", "version")
}
