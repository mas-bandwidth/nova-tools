package main

import (
	"bytes"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// reads and writes nothing (the CLI style's rule (b), #4505); `example` is
// handed a directory first, and the check holds that nothing was made there.
// The help-text assertions pin the three fixes the J05 cold read asked for:
// the stream split stated once, the all-skipped closing form listed, and the
// example setup line given a setup: heading.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, run, []testverbhelp.Case{
		{Verb: "version"},
		{Verb: "scan"},
		{Verb: "shapes"},
		{Verb: "example", Flags: []string{"--dry-run"}},
	})

	// The banner, by running `help` (ONBOARDING point 1: <tool> help prints usage).
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"help"}, &stdout, &stderr), "`nova-self-talk help` must exit 0; stderr: %s", stderr.String())
	text := stdout.String()

	// (1) The banner says once, above the list, which lines go to stderr and which
	// to stdout, rather than annotating only the one FAIL line.
	assert.Contains(t, text, "Findings print on stderr", "the banner must state the stderr/stdout split once, above the list")

	// (2) The all-skipped closing form is a third closing line in the list, with
	// the words that follow it as printed.
	assert.Contains(t, text, "SELFTALK SKIP files=0 skipped=<n> reason=all-skipped", "the banner must list SKIP files=0 as a closing form")

	// (3) The example setup line sits under a setup: heading, as nova-memory's
	// banner does, so the page says in its own grammar that the block depends on it.
	assert.Contains(t, text, "\nsetup:\n  nova-self-talk example ./pages\n", "the setup line must sit under a setup: heading")

	// The setup line runs as printed: nova-self-talk example <dir> exits 0 and writes the pages.
	var out, errb bytes.Buffer
	exit := run([]string{"example", t.TempDir()}, &out, &errb)
	require.Equal(t, 0, exit, "the setup line `nova-self-talk example <dir>` must run and exit 0: %s", errb.String())
	assert.Contains(t, out.String(), "EXAMPLE OK dir=", "the setup line must write the example pages: %s", out.String())
}
