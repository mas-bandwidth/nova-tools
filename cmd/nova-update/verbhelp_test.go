package main

import (
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads the manifest, runs a binary, dials the store or writes a
// snapshot (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	file := []string{"--file", "{dir}/manifest.tsv"}
	testverbhelp.Check(t, updateRun, []testverbhelp.Case{
		{Verb: "check", Flags: file},
		{Verb: "apply", Flags: file},
		{Verb: "report", Flags: append(file, "--snapshot", "{dir}/snap", "--store", "{addr}")},
		{Verb: "watch", Flags: []string{"--adopt", "{dir}/checks.tsv"}},
		{Verb: "adoption", Flags: file},
		{Verb: "release cut"},
		{Verb: "release build"},
		{Verb: "release install"},
		{Verb: "release adopt"},
		{Verb: "release pull"},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, updateRun, "nova-update", "check", "adoption", "version")
}

func updateRun(args []string, stdout, stderr io.Writer) int {
	return update.Run("nova-update", args, "", stdout, stderr, update.Environment{})
}
