package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
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
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-%04d.md", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
	dir := largeCorpus(t, 500)
	exit, stdout, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate")
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", exit, stderr)
	}
	if got := countLines(stderr); got != bounded.Default+2 {
		t.Errorf("stderr is %d lines, want %d findings + one MORE line + one count line:\n%s",
			got, bounded.Default, firstLines(stderr, 3))
	}
	if !strings.Contains(stderr, "VERIFY MORE kind=wikilink shown=20 total=500") {
		t.Errorf("no MORE line naming the total:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--fail-max") {
		t.Errorf("the MORE line names no remedy:\n%s", stderr)
	}
	// The count line on FAILURE is the half that was missing everywhere: N lines and
	// never N.
	if !strings.Contains(stderr, "VERIFY FAIL gating=500 shown=20") {
		t.Errorf("no count line on failure:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("a failing verify wrote to stdout: %q", stdout)
	}
}

// The remedy in the MORE line has to be true, which means running it.
func TestVerifyFailMaxWidensAndZeroPrintsAll(t *testing.T) {
	dir := largeCorpus(t, 500)
	_, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--fail-max", "5")
	if got := countLines(stderr); got != 7 {
		t.Errorf("--fail-max 5 gave %d lines, want 5 + MORE + count", got)
	}
	_, _, stderr = runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--fail-max", "0")
	if got := countLines(stderr); got != 501 {
		t.Errorf("--fail-max 0 gave %d lines, want all 500 + the count line", got)
	}
	if strings.Contains(stderr, "VERIFY MORE") {
		t.Errorf("--fail-max 0 elided nothing and must print no MORE line")
	}
}

// Zero already means all, so a negative ceiling is a typo with two readings and gets
// neither.
func TestVerifyRefusesANegativeCeiling(t *testing.T) {
	dir := largeCorpus(t, 3)
	exit, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--fail-max", "-1")
	if exit != 2 || !strings.Contains(stderr, "--fail-max must be a line ceiling") {
		t.Errorf("exit = %d, stderr = %q", exit, stderr)
	}
}

// The reason the cap is per KIND: twenty wikilink findings must not be able to eat the
// one frontmatter finding, which is the line the reader did not already know.
func TestVerifyCapsEachKindSeparately(t *testing.T) {
	dir := largeCorpus(t, 500)
	// One file that is missing its frontmatter name AND is the only one under this glob.
	if err := os.WriteFile(filepath.Join(dir, "index-of-things.md"), []byte("# index\n\nno name here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, stderr := runCLI(t, "", "verify", "--root", dir, "--links", "gate", "--frontmatter", "index-*.md")
	if !strings.Contains(stderr, "frontmatter") {
		t.Fatalf("the buried kind never printed:\n%s", stderr)
	}
	if !strings.Contains(stderr, "VERIFY MORE kind=wikilink") {
		t.Errorf("the loud kind was not capped:\n%s", stderr)
	}
	if strings.Contains(stderr, "VERIFY MORE kind=frontmatter") {
		t.Errorf("the quiet kind was capped though it had one finding:\n%s", stderr)
	}
}

// A flag typo used to cost the whole 62-line banner. It costs one line and names the door.
func TestAFlagTypoIsOneLine(t *testing.T) {
	for _, args := range [][]string{
		{"verify", "--rooot", "x", "--links", "gate"},
		{"frobnicate"},
		nil,
	} {
		exit, stdout, stderr := runCLI(t, "", args...)
		if exit != 2 {
			t.Errorf("%v: exit = %d, want 2", args, exit)
		}
		if got := countLines(stderr); got != 1 {
			t.Errorf("%v: the refusal is %d lines, want 1:\n%s", args, got, stderr)
		}
		if !strings.Contains(stderr, "run: nova-memory help") {
			t.Errorf("%v: the refusal names no door: %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, stdout)
		}
	}
	// And the door opens.
	exit, stdout, _ := runCLI(t, "", "help")
	if exit != 0 || !strings.Contains(stdout, "usage:") {
		t.Errorf("`help` did not print the usage: exit %d", exit)
	}
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
	dir := largeCorpus(t, 20)
	var gold strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&gold, "zzzqqq unrelated query %d\tno-such-file-%d.md\n", i, i)
	}
	path := filepath.Join(t.TempDir(), "gold.tsv")
	if err := os.WriteFile(path, []byte(gold.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runCLI(t, "", "eval", "--root", dir, "--channels", "bm25", "--k", "3", "--floor", "0.8", path)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (nothing can hit); stderr: %s", exit, stderr)
	}
	if got := countLines(stdout); got != bounded.Default+1 {
		t.Errorf("stdout is %d lines, want %d misses + one MORE line:\n%s", got, bounded.Default, firstLines(stdout, 3))
	}
	if !strings.Contains(stdout, "EVAL MORE kind=miss shown=20 total=500") {
		t.Errorf("no MORE line naming the total:\n%s", firstLines(stdout, 25))
	}
	if !strings.Contains(stderr, "misses=500 shown=20") {
		t.Errorf("the FAIL line does not carry the miss count: %q", stderr)
	}
}
