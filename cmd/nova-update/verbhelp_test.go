package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/internal/update"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads the manifest, runs a binary, dials the store or writes a
// snapshot (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	file := []string{"--file", "{dir}/manifest.tsv"}
	testverbhelp.Check(t, updateRun, []testverbhelp.Case{
		{Verb: "example", Flags: []string{"--out", "{dir}/versions.tsv"}},
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
// on the manifest `example --out` writes, exits 0: status on it, and apply --dry-run,
// which reads and never installs.
func TestStatusAndApplyHelpShowAnExampleThatRuns(t *testing.T) {
	t.Parallel()
	manifest := filepath.Join(t.TempDir(), "versions.tsv")
	var out, errs bytes.Buffer
	c := updateRun([]string{"example", "--out", manifest}, &out, &errs)
	require.Equal(t, 0, c, "example --out: %s", errs.String())
	for _, tc := range []struct{ verb, flag string }{{"status", ""}, {"apply", "--dry-run"}} {
		out.Reset()
		errs.Reset()
		{
			c := updateRun([]string{tc.verb, "-h"}, &out, &errs)
			require.Equal(t, 0, c, "%s -h exited %d: %s", tc.verb, c, errs.String())
		}
		var example string
		for _, l := range strings.Split(out.String(), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "nova-update "+tc.verb+" --file versions.tsv") {
				example = l
			}
		}
		require.NotEmpty(t, example, "%s -h shows no example line ending %q:\n%s", tc.verb, tc.flag, out.String())
		require.True(t, strings.HasSuffix(example, tc.flag), "%s -h shows no example line ending %q:\n%s", tc.verb, tc.flag, out.String())
		// The line names ./versions.tsv, the file example --out writes; the test's is in a temp dir.
		args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(example, "nova-update "), "versions.tsv", manifest))
		out.Reset()
		errs.Reset()
		{
			c := updateRun(args, &out, &errs)
			assert.Equal(t, 0, c, "the help example %q exits %d\nstdout: %s\nstderr: %s", example, c, out.String(), errs.String())
		}
	}
}
