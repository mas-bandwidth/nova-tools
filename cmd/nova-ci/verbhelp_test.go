package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads, runs, dials or writes anything (the CLI style's rule
// (b), #4505). functional's -h used to be a refusal at exit 2 (#4503, against
// silence); it is help now, which is not silence either.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, ciRun, []testverbhelp.Case{
		{Verb: "slowtests", Flags: []string{"--allowlist", "{dir}/allow.txt"}},
		{Verb: "local", Flags: []string{"--base", "origin/dev"}},
		{Verb: "functional"},
		{Verb: "new-rule", Flags: []string{"--root", "{dir}"}},
		{Verb: "new-verb", Flags: []string{"--root", "{dir}"}},
		{Verb: "github receipt", Flags: []string{"--redis", "{addr}", "--from-runner"}},
		{Verb: "github"},
		{Verb: "cost", Flags: []string{"--redis", "{addr}"}},
		{Verb: "queue", Flags: []string{"--repo", "mas-bandwidth/nova-tools"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, ciRun, "nova-ci", "slowtests", "functional", "cost", "queue", "version")
}

func ciRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr)
}
