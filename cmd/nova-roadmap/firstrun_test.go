package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: a bare
// command refuses and names its door, the banner's examples run in order, and
// docs/TESTS.md's first run is compared with what the tool prints by the one
// comparator. Every ./ path of a documented line is resolved in the test's own
// directory, so nothing is read or written in the checkout.

// sitting runs the tool with the documented ./ paths under a fresh directory.
func sitting(t *testing.T) (func(args []string) onboarding.Result, string) {
	t.Helper()
	dir := t.TempDir()
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			if rest, ok := strings.CutPrefix(a, "./"); ok {
				a = filepath.Join(dir, rest)
			}
			local[i] = a
		}
		var out, errb bytes.Buffer
		code := novaRoadmap().Run(local, strings.NewReader(""), &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	}, dir
}

// (a) A bare command refuses in one line, on stderr at exit 2, and names its door.
func TestABareCommandNamesItsDoor(t *testing.T) {
	t.Parallel()
	run, _ := sitting(t)
	res := run(nil)
	assert.Equal(t, 2, res.Code)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, 1, strings.Count(res.Stderr, "\n"), res.Stderr)
	assert.Contains(t, res.Stderr, "run: nova-roadmap help")
}

// (b) The docs/TESTS.md first run is EXECUTED, every command in order, and its
// whole output compared with the block; the one value not compared as written
// is the directory the ./ paths resolve in.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-roadmap")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-roadmap", lines)
	require.NoError(t, err)
	// the verbs a first run is: data written, the page rendered, the page
	// checked; a transcript that has quietly lost one still matches line for
	// line, so the sitting is held to them by name, and the banner's example
	// block is this same first run
	for _, verb := range []string{"nova-roadmap example", "nova-roadmap render", "nova-roadmap check"} {
		found := false
		for _, s := range steps {
			found = found || strings.HasPrefix(strings.Join(append([]string{"nova-roadmap"}, s.Args...), " "), verb+" ")
		}
		assert.True(t, found, "the docs/TESTS.md first run never runs `%s`", verb)
	}
	run, dir := sitting(t)
	banner := run([]string{"help"})
	examples, err := onboarding.ExampleLines(banner.Stdout, "nova-roadmap")
	require.NoError(t, err)
	documented := make([]string, 0, len(steps))
	for _, s := range steps {
		documented = append(documented, strings.Join(append([]string{"nova-roadmap"}, s.Args...), " "))
	}
	assert.Equal(t, strings.Join(documented, "\n"), strings.Join(examples, "\n"), "the banner's example block is not docs/TESTS.md's first run")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "tmpdir", Doc: ".", Run: dir}}))
	page, err := os.ReadFile(filepath.Join(dir, "ROADMAP.md"))
	require.NoError(t, err)
	assert.Contains(t, string(page), "| Connect tokens | ✅ |\n| IPv6 (later) | ❌ |\n| complete | 50% |\n")
}

// The banner's example block is a first run a stranger types in order: each
// line runs at exit 0, and the last, the check, passes on what render wrote.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	run, dir := sitting(t)
	banner := run([]string{"help"})
	require.Equal(t, 0, banner.Code, banner.Stderr)
	examples, err := onboarding.ExampleLines(banner.Stdout, "nova-roadmap")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(examples), onboarding.MinExampleCommands, "the example block: %q", examples)
	for _, ex := range examples {
		fields, err := onboarding.SplitShell(ex)
		require.NoError(t, err)
		res := run(fields[1:])
		assert.Equal(t, 0, res.Code, "the example %q exits %d: %s", ex, res.Code, res.Stderr)
	}
	again := run([]string{"example", "--out", filepath.Join(dir, "docs", "roadmap.sexp")})
	assert.Equal(t, 2, again.Code, "example never writes over data")
}
