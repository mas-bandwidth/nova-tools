package main

import (
	"bytes"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, run, "nova-check", "quickstart", "dogfood gate", "spelling", "version")
}

// The top help names the door to a verb's own help: a cold reader who stops at
// the one page learns there that `nova-check <verb> -h` and
// `nova-check help <verb>` print the verb's flags, its effect and exit codes
// (docs/STANDARD.md section 3, "Help is never a refusal"). The dispatch
// supports both forms; the banner must say so.
func TestTopHelpNamesTheVerbHelp(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	code := run([]string{"help"}, &out, &errb)
	require.EqualValues(t, 0, code, "exit %d, stderr %q", code, errb.String())
	help := out.String()
	for _, form := range []string{"nova-check <verb> -h", "nova-check help <verb>"} {
		assert.Contains(t, help, form, "the top help never names the form %q, so a reader stops at the one page", form)
	}
	assert.Contains(t, help, "the verb's flags, its effect and exit codes",
		"the top help does not say what the verb's own help prints")
}
