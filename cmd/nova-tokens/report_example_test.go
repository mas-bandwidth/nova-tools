package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestTheHelpReportExampleIsWhatItPrints runs the help's report line as a reader
// pastes it after the setup line above the block -- ./repos.tsv and ./transcripts
// a copy of the example bench -- through the comparator, at this package's fixed
// fold instant. The build word is the build's and is compared by shape.
func TestTheHelpReportExampleIsWhatItPrints(t *testing.T) {
	t.Parallel()

	line := "nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts"
	examples, err := onboarding.ExampleLines(usage, "nova-tokens")
	require.NoError(t, err, err)
	found := false
	for _, ex := range examples {
		found = found || ex == line
	}
	require.True(t, found, "the help's example block does not hold %q:\n  %s", line, strings.Join(examples, "\n  "))
	bench := t.TempDir()
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), bench)
	args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(line, "nova-tokens "), "./", bench+"/"))
	r := invoke(t, args...)
	wantExit(t, r, 0)
	step := onboarding.Step{Line: "$ " + line, Want: []string{
		`REPORT OK who=ada day=2026-09-11 rows=7 at=2026-09-11T23:55:02Z build=devel subject="tokens 2026-09-11 at=2026-09-11T23:55:02Z build=devel"`,
		"2026-09-11\tada\tclaude-fable-5-1\tschema\tinput\t908",
		"2026-09-11\tada\tclaude-fable-5-1\tschema\toutput\t1535",
		"2026-09-11\tada\tclaude-fable-5-1\tschema\tcache_write\t1200",
		"2026-09-11\tada\tclaude-fable-5-1\tschema\tcache_read\t242000",
		"2026-09-11\tada\tclaude-fable-5-1\tserialize\tinput\t430",
		"2026-09-11\tada\tclaude-fable-5-1\tserialize\toutput\t58",
		"2026-09-11\tada\tclaude-fable-5-1\tserialize\tcache_read\t4000",
		"! REPORT AVG day=2026-09-11 model=claude-fable-5-1 tokens=250131 usd=- usd_per_mtok=- unpriced=250131",
		"! REPORT AVG-ALL day=2026-09-11 tokens=250131 usd=- usd_per_mtok=- unpriced=250131",
	}}
	for _, p := range onboarding.Compare(step, onboarding.Result{Code: r.exit, Stdout: r.stdout, Stderr: r.stderr}, []onboarding.Norm{onboarding.Version()}) {
		assert.Fail(t, "onboarding example comparison failed", "%v", p)
	}
}
