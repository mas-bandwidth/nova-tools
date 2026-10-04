package main

import (
	"bytes"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/internal/update"

	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads the manifest, builds a revision, runs a binary or writes
// a snapshot (the CLI style's rule (b), #4505).
//
// The banner also says why report and send take no --json and shows the first
// line report prints, where the manifest's six fields are stated, and the exit
// codes by verb. A sentence here is held to the behaviour it describes by
// RUNNING the verb: the --json exception's sample is compared, byte for byte,
// against the first line `report` prints on the example manifest.
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

	help := helpText(t)
	for _, want := range []string{
		"Every verb but report, send takes --json",
		"report and send write the note body the bus carries, so they take no --json; report's first line is `",
		"THE MANIFEST is the file --file names, written by hand: one line per tool, six tab-separated fields name kind installed latest apply owner; report -h states its six rules.",
		"exit codes: 0 done, 2 usage or could not run, for every verb; by verb:",
		"  report: 0 ",
		"  send: 0 ",
		"  snapshot: 0 ",
		"  diff: 0 ",
		"  moved: 0 ",
	} {
		require.Contains(t, help, want, "`nova-version help` no longer says %q:\n%s", want, help)
	}
	// The 100-character placeholder is gone from the usage lines and the shape
	// sentence stands once, on its own line.
	require.NotContains(t, help, "--file <manifest: ", "the usage line still carries the placeholder:\n%s", help)
	require.Equal(t, 1, strings.Count(help, "six tab-separated fields name kind installed latest apply owner"),
		"the six-field sentence is stated once:\n%s", help)
	require.Contains(t, help, "  nova-version snapshot --file <manifest>\n", "the snapshot usage line is not --file <manifest>:\n%s", help)
	require.Contains(t, help, "  nova-version report --file <manifest> ", "the report usage line is not --file <manifest>:\n%s", help)
	require.Contains(t, help, "  nova-version send --file <manifest> ", "the send usage line is not --file <manifest>:\n%s", help)

	// The sample the help pastes is the first line `report` prints, byte for
	// byte, run on the example manifest the help's example block writes.
	m := regexp.MustCompile("report's first line is `([^`]+)`").FindStringSubmatch(help)
	require.NotNil(t, m, "the --json sentence no longer pastes report's first line:\n%s", help)
	sample := m[1]
	dir := t.TempDir()
	require.Equal(t, 0, versionRun([]string{"example", "--out", filepath.Join(dir, "versions.tsv")}, io.Discard, io.Discard))
	var out, errs bytes.Buffer
	code := versionRun([]string{"report", "--file", filepath.Join(dir, "versions.tsv")}, &out, &errs)
	require.Equal(t, 0, code, "report exit=%d stderr=%s", code, errs.String())
	first, _, _ := strings.Cut(out.String(), "\n")
	require.True(t, strings.HasPrefix(first, sample),
		"report's first line %q does not begin with the sample the help shows (%q)", first, sample)
}

// helpText returns `nova-version help` as printed, or fails.
func helpText(t *testing.T) string {
	t.Helper()
	var out, errs bytes.Buffer
	code := versionRun([]string{"help"}, &out, &errs)
	require.Equal(t, 0, code, "help exit=%d stderr=%s", code, errs.String())
	return out.String()
}

func versionRun(args []string, stdout, stderr io.Writer) int {
	return update.Run("nova-version", args, "", stdout, stderr, update.Environment{})
}
