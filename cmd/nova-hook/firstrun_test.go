//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstrun_test.go pins the onboarding standard for this binary: the
// docs/TESTS.md `### First run` transcript is RUN, line for line and in order,
// through the one comparator (onboarding.CompareTranscript), and the usage
// banner's `example:` block is that same sitting. The sitting runs on a
// throwaway redis-server, its address in NOVA_BUS_REDIS, which changes what the
// tool dials and nothing it prints; nothing in it is run-owned.

// documented runs this binary's own entry point with the documented
// arguments, the real store opened at the throwaway address.
func documented(addr string) onboarding.Runner {
	w := realWorld()
	w.getenv = func(k string) string {
		if k == bus.RedisEnv {
			return addr
		}
		return ""
	}
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb strings.Builder
		code := run(s.Args, strings.NewReader(""), &out, &errb, w)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	// The banner's example lines, named here so the pasted-examples rule
	// (SPEC-TOOLWORK.md documents rule 6) reads the command text in this test;
	// the transcript runs the same lines.
	documentedExamples := []string{
		"nova-hook status --as m1",
		"nova-hook hook --as m1 --role member --once",
		"nova-hook unhook --as m1",
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-hook")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-hook", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block of docs/TESTS.md holds no nova-hook command")
	run := documented(testredis.Start(t))
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		res, err := run(s)
		require.NoError(t, err)
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		assert.Failf(t, "documented transcript differs", "docs/TESTS.md: %s", p)
	}

	// the banner's example block is that same sitting, line for line
	var out, errb strings.Builder
	require.Equal(t, 0, run0([]string{"help"}, &out, &errb), errb.String())
	examples, err := onboarding.ExampleLines(out.String(), "nova-hook")
	require.NoError(t, err)
	var doc []string
	for _, s := range steps {
		doc = append(doc, strings.TrimPrefix(s.Line, "$ "))
	}
	assert.Equal(t, doc, examples, "the banner's example block is not docs/TESTS.md's first run")
	assert.Equal(t, documentedExamples, examples, "the banner's examples and the ones this test names are one list")
}

func run0(args []string, out, errb *strings.Builder) int {
	return run(args, strings.NewReader(""), out, errb, realWorld())
}
