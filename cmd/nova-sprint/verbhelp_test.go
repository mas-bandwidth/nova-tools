package main

import (
	"io"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them opens or mutates the directory it was pointed at (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	dir := []string{"--dir", "{dir}/sprint"}
	testverbhelp.Check(t, sprintRun, []testverbhelp.Case{
		{Verb: "init", Flags: dir},
		{Verb: "status", Flags: dir},
		{Verb: "step", Flags: append(dir, "--action", `{"type":"test"}`)},
		{Verb: "replay", Flags: dir},
		{Verb: "check", Flags: dir},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, sprintRun, "nova-sprint", "init", "status", "step", "replay", "check", "version")
}

func sprintRun(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, time.Now().UTC())
}
