package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestTheHelpReadingExamplesAreWhatTheyPrint runs the help's check, sum and sources lines
// as a reader pastes them after the fold above them, in a copy of the example bench, and
// compares each with what the tool prints. The stamp and the build word are the run's and
// are compared by shape.
func TestTheHelpReadingExamplesAreWhatTheyPrint(t *testing.T) {
	t.Parallel()

	examples, err := onboarding.ExampleLines(usage, "nova-tokens")
	require.NoError(t, err)
	bench := t.TempDir()
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), bench)
	mkdir(t, filepath.Join(bench, "out"))
	run := func(line string) onboarding.Result {
		args := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(line, "nova-tokens "), "./", bench+"/"))
		r := invoke(t, args...)
		local := func(s string) string { return strings.ReplaceAll(s, bench+"/", "./") }
		return onboarding.Result{Code: r.exit, Stdout: local(r.stdout), Stderr: local(r.stderr)}
	}
	require.Equal(t, 0, run("nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts").Code)

	build, err := onboarding.Elide("build id (the identity of this build)", `build=(devel|v[0-9]+\.[0-9]+\.[0-9]+[^ \t]*)`, "build=<this build>")
	require.NoError(t, err)
	norms := []onboarding.Norm{onboarding.Instant("at"), build}
	source := "SOURCES SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=3 dup=1 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=2"
	for _, step := range []onboarding.Step{
		{Line: "$ nova-tokens check --out ./out", Want: []string{
			"CHECK OK at=2026-09-11T23:55:02Z build=devel files=1 rows=2 first=2026-09-11 last=2026-09-11 missing=0 stray=0 gap=0 notes=0",
		}},
		{Line: "$ nova-tokens sum --out ./out --month 2026-09", Want: []string{
			"SUM OK month=2026-09 days=1 missing=0 pairs=2 models=1 nonutc=0",
			"SUM MONTH month=2026-09 at=2026-09-11T23:55:02Z build=devel days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=2 turns=3",
			"SUM PAIR model=claude-fable-5-1 repo=schema input=908 output=1535 cache_write=1200 cache_read=242000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1",
			"SUM PAIR model=claude-fable-5-1 repo=serialize input=430 output=58 cache_write=- cache_read=4000 reasoning=- rough=0 dashes=0,0,1,0,1 nonutc=0 days=1",
			"SUM MODEL model=claude-fable-5-1 input=1338 output=1593 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,1,0,2 nonutc=0 repos=2",
			"SUM TOTAL input=1338 output=1593 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,1,0,2 nonutc=0 turns=3 pairs=2 models=1",
		}},
		{Line: "$ nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts", Want: []string{
			"SOURCES OK sources=1 files=1 messages=3 unreadable=0 unparsed=0 rows=2 unattributed=-",
			source,
		}},
		{Line: "$ nova-tokens sources --repos ./repos.tsv --all --claude bench=./transcripts --unattributed --max 20", Want: []string{
			"SOURCES OK sources=1 files=1 messages=3 unreadable=0 unparsed=0 rows=2 unattributed=0",
			source,
		}},
	} {
		line := strings.TrimPrefix(step.Line, "$ ")
		assert.Contains(t, examples, line, "the help's example block does not hold %q", line)
		for _, p := range onboarding.Compare(step, run(line), norms) {
			assert.Fail(t, "onboarding example comparison failed", "%v", p)
		}
	}
}
