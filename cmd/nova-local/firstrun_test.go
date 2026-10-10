// First-run tests for nova-local: the banner's `example:` block and the docs/TESTS.md
// `### First run` transcript are RUN here, as one sitting, against a fake ollama whose
// weights are in the shared store of the AI root /ai, so they need no engine, no network
// and no real time (the warm-up's load= is the fake clock's).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sitting runs documented lines from a checkout root against one fake engine: $PWD is the
// checkout, and the description the first run writes (./gemma.json) is a file in the
// test's own directory.
func sitting(t *testing.T) (func(args []string) onboarding.Result, string) {
	dir := t.TempDir()
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	stand := strings.NewReplacer("$PWD/cmd/nova-local/testdata/", testdata+"/", "./gemma.json", filepath.Join(dir, "gemma.json"))
	run := rig(newFake(firstRunRoot+"/shared/models/ollama"), firstRunRoot)
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			local[i] = stand.Replace(a)
		}
		return onboarding.Result(run.Run(local...))
	}, dir
}

// The usage banner's examples are the three verbs in the order a first run types them,
// and each one runs and exits 0.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	examples, err := onboarding.ExampleLines(cli.OK(t, "help").Stdout, "nova-local")
	require.NoError(t, err)
	verbs := []string{"status", "serve", "worker"}
	require.Len(t, examples, len(verbs), "want one example of each of %v under `example:`, got %q", verbs, examples)
	run, _ := sitting(t)
	for i, verb := range verbs {
		fields, err := onboarding.SplitShell(examples[i])
		require.NoError(t, err)
		require.Equal(t, verb, fields[1], "example %d", i)
		res := run(fields[1:])
		assert.Equal(t, 0, res.Code, "the example %q exits %d: %s", examples[i], res.Code, res.Stderr)
	}
}

// The `### First run` block of docs/TESTS.md is EXECUTED, every command in order, and its
// whole output compared with the block by the one comparator; the one value it does not
// compare as written is the directory the description is written to.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	// The banner's example lines, named here so the pasted-examples rule
	// (SPEC-TOOLWORK.md documents rule 6) reads the command text in this test;
	// the transcript runs the same lines.
	documentedExamples := []string{
		"nova-local status",
		"nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7",
		"nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m",
	}
	examples, err := onboarding.ExampleLines(cli.OK(t, "help").Stdout, "nova-local")
	require.NoError(t, err)
	require.Equal(t, documentedExamples, examples, "the banner's examples and the ones this test names are one list")
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-local")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-local", lines)
	require.NoError(t, err)
	var commands []string
	for _, s := range steps {
		commands = append(commands, strings.TrimPrefix(s.Line, "$ "))
	}
	require.Equal(t, documentedExamples, commands, "the transcript runs the banner's examples")
	run, dir := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "tmpdir", Doc: ".", Run: dir}}))
}
