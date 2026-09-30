package main

import (
	"bytes"
	"io"
	"strings"
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
		{Verb: "status", Flags: file},
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
	testverbhelp.HelpVerb(t, updateRun, "nova-update", "check", "status", "adoption", "version")
}

func updateRun(args []string, stdout, stderr io.Writer) int {
	return update.Run("nova-update", args, "", stdout, stderr, update.Environment{})
}

// TestStatusAndApplyHelpShowAnExampleThatRuns: `status -h` and `apply -h` each quote a
// line from the tool's example block that uses the new spelling, and that line, run
// from the checkout as printed, exits 0: status on the shipped example manifest, and
// apply --dry-run on the dry-run fixture, which reads and never installs.
func TestStatusAndApplyHelpShowAnExampleThatRuns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ verb, flag string }{{"status", ""}, {"apply", "--dry-run"}} {
		var out, errs bytes.Buffer
		if c := updateRun([]string{tc.verb, "-h"}, &out, &errs); c != 0 {
			t.Fatalf("%s -h exited %d: %s", tc.verb, c, errs.String())
		}
		var example string
		for _, l := range strings.Split(out.String(), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "nova-update "+tc.verb+" --file cmd/") {
				example = l
			}
		}
		if example == "" || !strings.HasSuffix(example, tc.flag) {
			t.Fatalf("%s -h shows no example line ending %q:\n%s", tc.verb, tc.flag, out.String())
		}
		// The line is written for the checkout root; the test runs in this package's directory.
		args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(example, "nova-update "), "cmd/nova-update/testdata/", "testdata/"))
		out.Reset()
		errs.Reset()
		if c := updateRun(args, &out, &errs); c != 0 {
			t.Errorf("the help example %q exits %d\nstdout: %s\nstderr: %s", example, c, out.String(), errs.String())
		}
	}
}
