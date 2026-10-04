package main

import (
	"bytes"
	"io"
	"os"
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
	// rules 2 and 3; CLI-STYLE usage formatting).
	var helpOut, helpErr bytes.Buffer
	code := updateRun([]string{"help"}, &helpOut, &helpErr)
	require.Equal(t, 0, code, "help exited %d: %s", code, helpErr.String())
	banner := helpOut.String()

	// (1) Rule 5 says how apply argv is run (SPEC-UPDATE rule 3: a command is
	// argv, never a shell), and keeps the every-problem refusal (SPEC-UPDATE
	// rule 2: one run names every problem with its line).
	assert.Contains(t, banner,
		"apply is the argv that updates it, or none; it is split on single spaces, no quotes, no shell: a pipe, a glob or a $VAR is a literal argument")
	assert.Contains(t, banner,
		"a run prints EVERY problem of the file at once, each with its line, never the first alone")

	// The sentence is checked by running it, never by rewording: a pipe, a
	// glob and a $VAR stay literal arguments of the argv (split on single
	// spaces, executed directly, no shell). report prints each entry's raw
	// first stdout line, so a shell would have left none of these standing.
	t.Run("apply argv runs with no shell", func(t *testing.T) {
		t.Parallel()
		manifest := filepath.Join(t.TempDir(), "versions.tsv")
		rows := "name\tkind\tinstalled\tlatest\tapply\towner\n" +
			"pipetool\ttool\techo v8.8.8|cat\t-\tnone\tme\n" +
			"globtool\ttool\techo v7.7.7 *\t-\tnone\tme\n" +
			"vartool\ttool\techo v6.6.6 $HOME\t-\tnone\tme\n"
		require.NoError(t, os.WriteFile(manifest, []byte(rows), 0o644))
		var out, errs bytes.Buffer
		c := updateRun([]string{"report", "--file", manifest}, &out, &errs)
		require.Equal(t, 0, c, "report exited %d: %s", c, errs.String())
		// oneline.Field escapes the space as \x20; |, * and $ stand.
		for _, want := range []string{"raw=v8.8.8|cat", `raw=v7.7.7\x20*`, `raw=v6.6.6\x20$HOME`} {
			assert.Contains(t, out.String(), want, "no-shell split kept the argument literal:\n%s", out.String())
		}
	})

	// (2) The post-usage note is one short paragraph per subject (defaults;
	// --json; report; status; apply; the send pointer), so no paragraph mixes
	// unrelated sentences, and pending and artifact are defined in report's
	// own -h, which the banner points at.
	assert.Regexp(t, `(?m)^Defaults: --max 20 .*\n\nEvery verb but watch and release takes --json:`, banner)
	assert.Regexp(t, `(?m)^Every verb but watch and release takes --json:.*\n\nreport needs no bus or network`, banner)
	assert.Regexp(t, `(?m)^report needs no bus or network.*\n\nstatus is check with every entry shown, current ones too\.`, banner)
	assert.Contains(t, banner, "Updates require an explicit apply name. apply --dry-run prints the plan and writes nothing.")
	assert.Contains(t, banner, "see `nova-update report -h` for --snapshot, pending and artifact")
	assert.NotContains(t, banner, "Do not prepare again while pending")
	assert.NotContains(t, banner, "sibling .lock file")

	// The report verb's help defines pending and artifact in one clause each,
	// and names what --snapshot is: the state file that carries the prepared
	// note (the flag's own words; snapshot.go's artifact is the pending
	// record's saved note inside that file).
	var reportOut, reportErr bytes.Buffer
	code = updateRun([]string{"report", "-h"}, &reportOut, &reportErr)
	require.Equal(t, 0, code, "report -h exited %d: %s", code, reportErr.String())
	reportHelp := reportOut.String()
	assert.Contains(t, reportHelp, "A prepared note not yet confirmed is pending")
	assert.Contains(t, reportHelp, "the artifact is that saved note")
	assert.Contains(t, reportHelp, "--snapshot names the state file that carries it")
	assert.Contains(t, reportHelp, "sibling .lock file for a kernel lock")
	assert.NotContains(t, reportHelp, "--snapshot names the artifact that carries it")

	// (3) The usage block is indented two spaces and wraps at 100 columns with
	// one continuation column per wrapped entry, as nova-ci's usage does; the
	// release entry keeps its words.
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
	assert.Contains(t, strings.Join(strings.Fields(usageBlock), " "),
		"nova-tools' own release pipeline: nova-update help release prints its usage lines")
	// One continuation column per wrapped entry: the report entry's wrapped
	// lines share one indent.
	var cont []string
	inReport := false
	for _, l := range strings.Split(usageBlock, "\n") {
		switch {
		case strings.HasPrefix(l, "  nova-update report --file"):
			inReport = true
		case inReport && (strings.HasPrefix(l, "  nova-update ") || strings.TrimSpace(l) == ""):
			inReport = false
		case inReport:
			cont = append(cont, l)
		}
	}
	require.NotEmpty(t, cont, "the report usage entry wraps")
	indent := len(cont[0]) - len(strings.TrimLeft(cont[0], " "))
	for _, l := range cont[1:] {
		assert.Equal(t, indent, len(l)-len(strings.TrimLeft(l, " ")), "one continuation column: %q", l)
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
