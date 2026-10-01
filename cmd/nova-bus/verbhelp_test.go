package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// neither touches the bus it was pointed at (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	bus := []string{"--bus", "{dir}/bus"}
	var cases []testverbhelp.Case
	for _, v := range []string{"draft", "prepare", "send", "reply", "inbox", "wait", "receipt", "close", "check", "names"} {
		cases = append(cases, testverbhelp.Case{Verb: v, Flags: bus})
	}
	cases = append(cases, testverbhelp.Case{Verb: "version"})
	cases = append(cases, testverbhelp.Case{Verb: "quickstart", Flags: []string{"--dir", "{dir}"}})
	testverbhelp.Check(t, busRun, cases)
	testverbhelp.HelpVerb(t, busRun, "nova-bus", "send", "inbox", "version", "quickstart")
}

func busRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr, now())
}
