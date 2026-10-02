package main

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The documented source-free fold example is compared with the real typed output from
// the known transcript fixture, so a flag, accounting or output change updates the contract.
func TestHelpFoldExampleIsComparedToOutput(t *testing.T) {
	t.Parallel()

	line := "nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts"
	examples, err := onboarding.ExampleLines(usage, "nova-tokens")
	require.NoError(t, err)
	assert.Contains(t, examples, line)

	bin := buildExampleBinary(t)
	root := t.TempDir()
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), root)
	mkdir(t, filepath.Join(root, "out"))
	exit, output := runExampleLine(t, root, filepath.Dir(bin), line)
	step := onboarding.Step{Line: "$ " + line, Want: []string{
		"TOKENS FOLD at=2026-09-11T23:55:02Z build=devel out=./out sources=1 days=2026-09-11 repos=./repos.tsv",
		"TOKENS SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=3 dup=1 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=2",
		"TOKENS DAY date=2026-09-11 rows=2 models=1 repos=2 turns=3 unknown=0.0% other=0.0% rough=0 dashes=3 nonutc=0 sources=claude:bench written=true",
		"TOKENS OK days=1 rows=2 sources=1 unreadable=0 unparsed=0 mixed=0 conflict=0 shrank=0 partial=0 quiet=0",
		"TOKENS NOTE nothing was wrong; nova-tokens check --out ./out is the gate",
	}}
	// The built binary stamps the instant it ran and the build it is: both are the run's.
	assert.Empty(t, onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{{Code: exit, Stdout: output}},
		[]onboarding.Field{{Name: "at"}, {Name: "build"}}))
}
