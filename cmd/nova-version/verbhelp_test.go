package main

import (
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads the manifest, builds a revision, runs a binary or writes
// a snapshot.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	file := []string{"--file", "{dir}/manifest.tsv"}
	testverbhelp.Check(t, versionRun, []testverbhelp.Case{
		{Verb: "example", Flags: []string{"--out", "{dir}/versions.tsv"}},
		{Verb: "moved", Flags: []string{"--repo", "{dir}/repo", "--out", "{dir}/moved.md"}},
		{Verb: "snapshot", Flags: []string{"--bin", "{dir}/bin", "--out", "{dir}/snap.tsv"}},
		{Verb: "diff", Flags: []string{"--from", "{dir}/a.tsv"}},
		{Verb: "report", Flags: append(file, "--snapshot", "{dir}/snap")},
		{Verb: "send", Flags: append(file, "--bus", "{dir}/bus")},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, versionRun, "nova-version", "moved", "snapshot", "send", "version")
}

func versionRun(args []string, stdout, stderr io.Writer) int {
	return update.Run("nova-version", args, "", stdout, stderr, update.Environment{})
}
