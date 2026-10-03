package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// largeCorpus builds the state the audit measured this binary at: a corpus whose every
// entry holds an unresolved wikilink and carries no frontmatter name, which is what a
// real corpus looks like on the day someone first points verify at it. n=500 here rather
// than the audit's 5,000 for the sake of the test's own runtime; the shape of the output
// is the same at either, which is the whole claim.
func largeCorpus(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("# entry %d\n\nA sentence about lanterns, and a link to [[no-such-entry-%d]].\n", i, i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-%04d.md", i)), []byte(body), 0o644))
	}
	return dir
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

// THE WORST LINE IN THE REPO, bounded. Five hundred unresolved wikilinks used to be five
// hundred lines and no total; at the audit's 5,000-entry state it was 10,000 lines and
// ~197K tokens, which is a context window spent to learn one number.
func TestVerifyCapsFindingsAndAlwaysPrintsTheCount(t *testing.T) {
	t.Parallel()

	dir := largeCorpus(t, 500)
	exit, stdout, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate")
	require.Equalf(t, 1, exit, "exit = %d, want 1; stderr: %s", exit, stderr)
	{
		got := countLines(stderr)
		assert.Equalf(t, bounded.Default+2, got, "stderr is %d lines, want %d findings + one MORE line + one count line:\n%s",
			got, bounded.Default, firstLines(stderr, 3))
	}
	assert.Containsf(t, stderr, "VERIFY MORE kind=wikilink shown=20 total=500", "no MORE line naming the total:\n%s", stderr)
	assert.Containsf(t, stderr, "--fail-max", "the MORE line names no remedy:\n%s", stderr)
	// The count line on FAILURE is the half that was missing everywhere: N lines and
	// never N.
	assert.Containsf(t, stderr, "VERIFY FAIL gating=500 shown=20", "no count line on failure:\n%s", stderr)
	assert.Equalf(t, "", stdout, "a failing verify wrote to stdout: %q", stdout)
}

// The remedy in the MORE line has to be true, which means running it.
func TestVerifyFailMaxWidensAndZeroPrintsAll(t *testing.T) {
	t.Parallel()

	dir := largeCorpus(t, 500)
	_, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--fail-max", "5")
	{
		got := countLines(stderr)
		assert.Equalf(t, 7, got, "--fail-max 5 gave %d lines, want 5 + MORE + count", got)
	}
	_, _, stderr = runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--fail-max", "0")
	{
		got := countLines(stderr)
		assert.Equalf(t, 501, got, "--fail-max 0 gave %d lines, want all 500 + the count line", got)
	}
	assert.NotContainsf(t, stderr, "VERIFY MORE", "--fail-max 0 elided nothing and must print no MORE line")
}

// Zero already means all, so a negative ceiling is a typo with two readings and gets
// neither.
func TestVerifyRefusesANegativeCeiling(t *testing.T) {
	t.Parallel()

	dir := largeCorpus(t, 3)
	exit, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--fail-max", "-1")
	assert.Equalf(t, 2, exit, "exit = %d, stderr = %q", exit, stderr)
	assert.Containsf(t, stderr, "--fail-max must be a line ceiling", "exit = %d, stderr = %q", exit, stderr)
}

// The reason the cap is per KIND: twenty wikilink findings must not be able to eat the
// one frontmatter finding, which is the line the reader did not already know.
func TestVerifyCapsEachKindSeparately(t *testing.T) {
	t.Parallel()

	dir := largeCorpus(t, 500)
	// One file that is missing its frontmatter name AND is the only one under this glob.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index-of-things.md"), []byte("# index\n\nno name here\n"), 0o644))
	_, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--frontmatter", "index-*.md")
	require.Containsf(t, stderr, "frontmatter", "the buried kind never printed:\n%s", stderr)
	assert.Containsf(t, stderr, "VERIFY MORE kind=wikilink", "the loud kind was not capped:\n%s", stderr)
	assert.NotContainsf(t, stderr, "VERIFY MORE kind=frontmatter", "the quiet kind was capped though it had one finding:\n%s", stderr)
}

// A flag typo used to cost the whole 62-line banner. It costs one line and names the door.
func TestAFlagTypoIsOneLine(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"verify", "--rooot", "x", "--links", "gate"},
		{"frobnicate"},
		{"stats", "--root", corpus, "extra"},
		{"verify", "--root", corpus, "--links", "gate", "extra"},
		{"quickstart", "--root", corpus, "extra"},
		nil,
	} {
		exit, stdout, stderr := runCLI(t, "", args...)
		assert.Equalf(t, 2, exit, "%v: exit = %d, want 2", args, exit)
		{
			got := countLines(stderr)
			assert.Equalf(t, 1, got, "%v: the refusal is %d lines, want 1:\n%s", args, got, stderr)
		}
		door := "run: nova-memory help"
		if len(args) > 1 && args[1] == "--rooot" {
			door = "run: nova-memory verify -h"
		}
		assert.Containsf(t, stderr, door, "%v: the refusal names no door: %q", args, stderr)
		assert.Equalf(t, "", stdout, "%v: a refusal wrote to stdout: %q", args, stdout)
	}
	// And the door opens.
	exit, stdout, _ := runCLI(t, "", "help")
	assert.Equalf(t, 0, exit, "`help` did not print the usage: exit %d", exit)
	assert.Containsf(t, stdout, "usage:", "`help` did not print the usage: exit %d", exit)
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}

// eval at the audit's state: five hundred gold rows, all missing. It printed one line
// per row — 36 KB — of which the passing half said only what the summary line says.
func TestEvalListsMissesOnlyAndCapsThem(t *testing.T) {
	t.Parallel()

	dir := largeCorpus(t, 20)
	var gold strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&gold, "zzzqqq unrelated query %d\tno-such-file-%d.md\n", i, i)
	}
	path := filepath.Join(t.TempDir(), "gold.tsv")
	require.NoError(t, os.WriteFile(path, []byte(gold.String()), 0o644))
	exit, stdout, stderr := runCLI(t, "", "eval", "--root", dir, "--channels", "bm25", "--k", "3", "--floor", "0.8", path)
	require.Equalf(t, 1, exit, "exit = %d, want 1 (nothing can hit); stderr: %s", exit, stderr)
	{
		got := countLines(stdout)
		assert.Equalf(t, bounded.Default+1, got, "stdout is %d lines, want %d misses + one MORE line:\n%s", got, bounded.Default, firstLines(stdout, 3))
	}
	assert.Containsf(t, stdout, "EVAL MORE kind=miss shown=20 total=500", "no MORE line naming the total:\n%s", firstLines(stdout, 25))
	assert.Containsf(t, stderr, "misses=500 shown=20", "the FAIL line does not carry the miss count: %q", stderr)
}
