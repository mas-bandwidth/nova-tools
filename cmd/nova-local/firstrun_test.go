// First-run tests for nova-local: the banner's `example:` block is RUN here, as one
// sitting, against a fake ollama whose weights are in the shared store of the AI root
// /ai, so it needs no engine, no network and no real time (the warm-up's load= is the
// fake clock's).
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
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
