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
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, updateRun, "nova-update", "check", "status", "adoption", "version")

	// The banner's own lines, as the J05 cold rating read them: the usage lines
	// are indented and wrapped like nova-ci's (ONBOARDING point 6 keeps the
	// block readable), the manifest's rule 5 says how the apply argv is run, in
	// rule 3's words (SPEC-UPDATE rule 3: a command is argv, never a shell),
	// and the notes are one short paragraph per subject, with pending and
	// artifact each named in one clause.
	t.Run("banner", func(t *testing.T) {
		t.Parallel()
		var out, errs bytes.Buffer
		code := updateRun([]string{"help"}, &out, &errs)
		require.Equal(t, 0, code, "help exited %d: %s", code, errs.String())
		banner := out.String()
		assert.Contains(t, banner, "5. apply is the argv that updates it, or none, split on single spaces, no quotes, no shell: a pipe, a glob or a $VAR is a literal argument",
			"rule 5 says how the apply argv runs")
		for _, para := range []string{
			"Defaults: --max 20 (0 = all), --timeout 5s, --budget 60s. Repeat --kind to select kinds.",
			"Every verb but watch takes --json: the same result as one JSON object on stdout. A result's first line is the verb, OK, FAIL or REFUSED, and the run's counts; `<verb> -h` lists a verb's flags and effect.",
			"Report needs no bus or network. Updates require an explicit apply name. status is check with every entry shown, current ones too. apply --dry-run prints the plan and writes nothing.",
			"A delivery is one nova-bus send on the Redis bus (nova-bus reads its store from NOVA_BUS_REDIS); with --snapshot, a report unchanged since it was confirmed sent to the same recipients is not sent again.",
			"A snapshot uses a sibling .lock file for a kernel lock; its presence never means a process is running.",
		} {
			assert.Contains(t, banner, "\n"+para+"\n", "the notes are one short paragraph per subject")
		}
		in, usage := false, 0
		for _, l := range strings.Split(banner, "\n") {
			switch {
			case l == "usage:":
				in = true
				continue
			case !in:
				continue
			case strings.TrimSpace(l) == "":
				in = false
				continue
			}
			usage++
			assert.True(t, strings.HasPrefix(l, "  "), "a usage line stands at column one: %q", l)
			assert.LessOrEqual(t, len(l), 100, "a usage line runs past 100 columns (%d): %q", len(l), l)
			if !strings.HasPrefix(l, "  nova-update ") {
				assert.True(t, strings.HasPrefix(l, "    "), "a wrapped line's continuation is not indented deeper: %q", l)
			}
		}
		assert.Greater(t, usage, 0, "the banner carries no usage block")
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
