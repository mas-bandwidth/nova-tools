package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
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

	// The three costliest lines of `nova-update help` are fixed (SPEC-UPDATE
	// rules 2, 3 and 5; CLI-STYLE usage formatting).
	var helpOut, helpErr bytes.Buffer
	code := updateRun([]string{"help"}, &helpOut, &helpErr)
	require.Equal(t, 0, code, "help exited %d: %s", code, helpErr.String())
	banner := helpOut.String()

	// (1) The apply column says how it is run: argv, split on spaces, no shell.
	assert.Contains(t, banner,
		"apply is the argv that updates it, or none; it is split on single spaces, no quotes, no shell: a pipe, a glob or a $VAR is a literal argument")

	// (2) The post-usage note is split into one paragraph per subject (defaults;
	// --json; report and send recovery), with the recovery terms defined in
	// report's own -h and pointed to from the banner.
	assert.Regexp(t, `(?m)^Defaults: --max 20 .*\n\nEvery verb but watch and release takes --json:`, banner)
	assert.Regexp(t, `(?m)^Every verb but watch and release takes --json:.*\n\nreport needs no bus or network`, banner)
	assert.Contains(t, banner, "see `nova-update report -h` for --snapshot, pending and artifact")

	// The report verb's help defines pending and artifact in one clause each.
	var reportOut, reportErr bytes.Buffer
	code = updateRun([]string{"report", "-h"}, &reportOut, &reportErr)
	require.Equal(t, 0, code, "report -h exited %d: %s", code, reportErr.String())
	reportHelp := reportOut.String()
	assert.Contains(t, reportHelp, "A prepared note that has not yet been confirmed is pending")
	assert.Contains(t, reportHelp, "--snapshot names the artifact that carries it")

	// (3) The usage block is indented two spaces and wraps at 100 columns with a
	// continuation indent, as nova-ci's usage does.
	usageStart := strings.Index(banner, "\nusage:\n")
	require.GreaterOrEqual(t, usageStart, 0, "banner has no usage: block")
	usageTail := banner[usageStart:]
	versionLine := strings.Index(usageTail, "\nnova-update version")
	require.GreaterOrEqual(t, versionLine, 0, "usage block is not followed by the version line")
	usageBlock := usageTail[:versionLine]
	require.Greater(t, strings.Count(usageBlock, "\n"), 10,
		"the long report and watch usage lines are wrapped")
	for _, l := range strings.Split(usageBlock, "\n") {
		if strings.TrimSpace(l) == "" || l == "usage:" {
			continue
		}
		assert.True(t, strings.HasPrefix(l, "  "), "every usage line is indented two spaces: %q", l)
		assert.LessOrEqual(t, len(l), 100, "usage lines wrap at 100 columns: %q", l)
	}

	// The example: block runs as printed (ONBOARDING point 1).
	t.Run("help examples run as printed", func(t *testing.T) {
		t.Parallel()
		var out, errs bytes.Buffer
		c := updateRun([]string{"help"}, &out, &errs)
		require.Equal(t, 0, c, "help exited %d: %s", c, errs.String())
		lines, err := onboarding.ExampleLines(out.String(), "nova-update")
		require.NoError(t, err, "parsing example lines")
		manifest := filepath.Join(t.TempDir(), "versions.tsv")
		for _, line := range lines {
			line = strings.TrimPrefix(line, "nova-update ")
			line = strings.ReplaceAll(line, "versions.tsv", manifest)
			args := strings.Fields(line)
			out.Reset()
			errs.Reset()
			c := updateRun(args, &out, &errs)
			assert.Equal(t, 0, c, "example line %q exited %d\nstdout: %s\nstderr: %s", line, c, out.String(), errs.String())
		}
	})
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
