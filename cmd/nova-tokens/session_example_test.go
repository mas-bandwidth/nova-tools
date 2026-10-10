package main

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheHelpSessionExampleIsWhatItPrints runs the help's session example as a reader pastes
// it after the banner's setup block and compares the two lines it prints. On internal/tool the
// banner's example block is every verb's Example, so this line is a counted help example and
// needs its own comparator entry (internal/ci/testdata/compared_examples.txt).
func TestTheHelpSessionExampleIsWhatItPrints(t *testing.T) {
	t.Parallel()

	line := "nova-tokens session --claude-session ./session.jsonl --out ./out"
	examples, err := onboarding.ExampleLines(usage, "nova-tokens")
	require.NoError(t, err)
	assert.Contains(t, examples, line, "the help's example block does not hold %q", line)

	bin := buildExampleBinary(t)
	root := t.TempDir()
	for _, setup := range fixtureSetupLines(usage) {
		exit, out := runExampleLine(t, root, filepath.Dir(bin), setup)
		require.Equal(t, 0, exit, "the fixture setup line exits %d, want 0:\n  %s\nits first output line: %s",
			exit, setup, exampleFirstLine(out))
	}

	exit, output := runExampleLine(t, root, filepath.Dir(bin), line)
	step := onboarding.Step{Line: "$ " + line, Want: []string{
		"SESSION OK turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012",
		"SESSION DAY day=2026-09-11 written=true rows=1 retained=0 model=claude-fable-5-1 weighted=11512",
	}}
	assert.Empty(t, onboarding.Compare(step, onboarding.Result{Code: exit, Stdout: output}, nil))
}
