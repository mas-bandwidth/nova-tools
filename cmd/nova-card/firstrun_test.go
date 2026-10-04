package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: the
// banner's examples are RUN, every refusal names its door, and docs/TESTS.md's
// first run is compared against what the tool prints, line for line.

func runCard(args ...string) (exit int, stdout, stderr string) {
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

// documented runs this binary's entry point with the documented arguments, the
// two documented paths mapped onto this test's own: the fixture is typed from the
// root of a checkout and ./cards is a directory the run makes. The norms reduce
// what the tool prints back to the documented spelling. No chdir, so the test
// runs in parallel (internal/ci/parallel_class_test.go).
func documented(t *testing.T) (onboarding.Runner, []onboarding.Norm) {
	t.Helper()
	cards := filepath.Join(t.TempDir(), "cards")
	rewrite := strings.NewReplacer("./cmd/nova-card/testdata", "testdata", "./cards", cards)
	run := func(s onboarding.Step) (onboarding.Result, error) {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = rewrite.Replace(a)
		}
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
	return run, []onboarding.Norm{onboarding.Path("./cmd/nova-card/testdata", "testdata"), onboarding.Path("./cards", cards)}
}

// (a) A bare command refuses in one line and names its door.
func TestABareCommandNamesItsDoor(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runCard()
	assert.Equal(t, 2, exit)
	assert.Empty(t, stdout)
	assert.Equal(t, 1, strings.Count(stderr, "\n"))
	assert.Contains(t, stderr, "run: nova-card help")
	exit, stdout, _ = runCard("help")
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, preAlpha)
	for _, verb := range []string{"generate", "lint", "template", "version"} {
		exit, stdout, stderr := runCard(verb, "-h")
		assert.Equal(t, 0, exit, "%s -h: %s", verb, stderr)
		assert.Contains(t, stdout, "effect: ", "%s -h names its effect", verb)
	}
}

// (b) The docs/TESTS.md first run is what the tool prints, and the banner's
// example block is the same list of commands.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-card")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-card", lines)
	require.NoError(t, err)
	require.Len(t, steps, 3)
	run, norms := documented(t)
	for _, p := range onboarding.Execute(steps, run, norms...) {
		t.Error(p)
	}
	examples, err := onboarding.ExampleLines(usage, "nova-card")
	require.NoError(t, err)
	var documented []string
	for _, s := range steps {
		documented = append(documented, "nova-card "+strings.Join(s.Args, " "))
	}
	assert.Equal(t, documented, examples, "the banner's example block and the first run are one list")
}

// A red brief is named on its LINT DRIFT line, and generate writes nothing while
// one brief is red.
func TestARedBriefIsNamedAndNothingIsWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	card := filepath.Join(dir, "bad.md")
	require.NoError(t, os.WriteFile(card, []byte("RESULT: bad sha=0123456789ab tier: cheap\nKIND: fix-red\n\nTHE TASK. <fill>\n"), 0o644))
	exit, stdout, _ := runCard("lint", "--card", card)
	assert.Equal(t, 1, exit)
	assert.Contains(t, stdout, "LINT DRIFT card=bad check=model-lines line=1")
	assert.Contains(t, stdout, "check=placeholder")

	// a PATHS entry that names nothing in the checkout is a red line too
	repo := t.TempDir()
	findings := filepath.Join(dir, "f.tsv")
	require.NoError(t, os.WriteFile(findings, []byte("internal/none/x.go:1\tgone\tfix it\tinternal/none TestX\n"), 0o644))
	out := filepath.Join(dir, "cards")
	exit, stdout, stderr := runCard("generate", "--from", "findings", "--file", findings, "--repo-dir", repo, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out)
	assert.Equal(t, 1, exit, stderr)
	assert.Contains(t, stdout, "check=paths-at-base")
	assert.NoDirExists(t, out)

	// an --out that already holds a brief is refused
	exit, _, stderr = runCard("generate", "--from", "findings", "--file", "testdata/findings.tsv", "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", dir)
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "already holds")
}

func TestRepoOfURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "mas-bandwidth/nova-tools", repoOfURL("git@github.com:mas-bandwidth/nova-tools.git"))
	assert.Equal(t, "mas-bandwidth/nova-tools", repoOfURL("https://github.com/mas-bandwidth/nova-tools"))
	assert.Equal(t, "", repoOfURL("nonsense"))
}
