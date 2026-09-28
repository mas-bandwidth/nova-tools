package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads a draft, writes the store or sends anything
// (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	drafts := []string{"--drafts", "{dir}/drafts"}
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "draft", Flags: drafts},
		{Verb: "show", Flags: drafts},
		{Verb: "send", Flags: drafts},
		{Verb: "hook"},
		{Verb: "issue"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, run, "nova-post", "draft", "send", "version")
}
