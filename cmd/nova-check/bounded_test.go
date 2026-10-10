package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	largeSelf500Dir  string
	largeSelf500Once sync.Once
)

// largeSelf builds the state the audit measured this binary at: a self repo with n broken
// markdown links and n code files, which is what an unchecked repo looks like on the day
// a stranger first runs quickstart against it.
func largeSelf(t *testing.T, n int) string {
	t.Helper()
	if n == 500 {
		largeSelf500Once.Do(func() {
			dir, err := os.MkdirTemp("", "large-self-500-*")
			if err != nil {
				panic(err)
			}
			for i := 0; i < 500; i++ {
				md := fmt.Sprintf("# page %d\n\nA [link](./no-such-page-%d.md) that does not resolve.\n", i, i)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("page-%04d.md", i)), []byte(md), 0o644); err != nil {
					panic(err)
				}
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("tool-%04d.py", i)), []byte("print(1)\n"), 0o644); err != nil {
					panic(err)
				}
			}
			largeSelf500Dir = dir
		})
		return largeSelf500Dir
	}
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		md := fmt.Sprintf("# page %d\n\nA [link](./no-such-page-%d.md) that does not resolve.\n", i, i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("page-%04d.md", i)), []byte(md), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("tool-%04d.py", i)), []byte("print(1)\n"), 0o644))
	}
	return dir
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

func TestLinksCapsFindingsAndAlwaysPrintsTheCount(t *testing.T) {
	t.Parallel()

	dir := largeSelf(t, 500)
	exit, stdout, stderr := runCheck(t, "links", "--dir", dir)
	require.EqualValues(t, 1, exit, "exit = %d, want 1; stderr: %s", exit, stderr)
	{
		got := countLines(stderr)
		assert.EqualValues(t, bounded.Default+2, got, "stderr is %d lines, want %d findings + MORE + count", got, bounded.Default)
	}
	assert.Contains(t, stderr, "LINKS MORE kind=broken shown=20 total=500", "no MORE line naming the total:\n%s", stderr)
	// The count line used to print only on PASS, so a run that found 800 broken links
	// said nothing about 800 and stdout was empty.
	assert.Contains(t, stderr, "broken=500", "no count line on failure:\n%s", stderr)
	assert.EqualValues(t, "", stdout, "a failing links wrote to stdout: %q", stdout)
}

func TestNoCodeCapsFindingsAndAlwaysPrintsTheCount(t *testing.T) {
	t.Parallel()

	dir := largeSelf(t, 500)
	exit, _, stderr := runCheck(t, "nocode", "--dir", dir)
	require.EqualValues(t, 1, exit, "exit = %d, want 1; stderr: %s", exit, stderr)
	{
		got := countLines(stderr)
		assert.EqualValues(t, bounded.Default+2, got, "stderr is %d lines, want %d findings + MORE + count:\n%s", got, bounded.Default, stderr)
	}
	assert.Contains(t, stderr, "NOCODE MORE kind=finding shown=20 total=500", "no MORE line naming the total:\n%s", stderr)
	assert.Contains(t, stderr, "findings=500", "no count line on failure:\n%s", stderr)
}

// THE FIRST-RUN VERB. Uncapped it was 1,400 lines for two lines of verdict — the most
// expensive thing a stranger could type, on the run where they know the least.
func TestQuickstartInheritsTheCaps(t *testing.T) {
	t.Parallel()

	dir := largeSelf(t, 500)
	exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir)
	require.EqualValues(t, 1, exit, "exit = %d, want 1; stderr: %s", exit, stderr)
	total := countLines(stdout) + countLines(stderr)
	assert.LessOrEqual(t, total, 50, "a first run on a 1,000-file repo costs %d lines; it should cost about forty", total)
	assert.True(t, strings.Contains(stderr, "LINKS MORE") && strings.Contains(stderr, "NOCODE MORE"), "quickstart did not pass the cap down to both checks:\n%s", stderr)
	assert.Contains(t, stdout, "QUICKSTART FAILED checks=2", "the closing line is missing: %q", stdout)
}

// The remedy has to be true, which means running it.
func TestFailMaxWidensAndZeroPrintsAll(t *testing.T) {
	t.Parallel()

	dir := largeSelf(t, 500)
	_, _, stderr := runCheck(t, "links", "--dir", dir, "--max", "5")
	{
		got := countLines(stderr)
		assert.EqualValues(t, 7, got, "--max 5 gave %d lines, want 5 + MORE + count", got)
	}
	_, _, stderr = runCheck(t, "links", "--dir", dir, "--max", "0")
	{
		got := countLines(stderr)
		assert.EqualValues(t, 501, got, "--max 0 gave %d lines, want all 500 + the count line", got)
	}
	assert.NotContains(t, stderr, "LINKS MORE", "--max 0 elided nothing and must print no MORE line")
	// quickstart passes its own ceiling down, including 0.
	_, _, stderr = runCheck(t, "quickstart", "--dir", dir, "--max", "0")
	{
		got := countLines(stderr)
		assert.EqualValues(t, 1002, got, "quickstart --max 0 gave %d lines, want 500+1 links and 500+1 nocode", got)
	}
}

func TestRefusesANegativeCeiling(t *testing.T) {
	t.Parallel()

	dir := largeSelf(t, 2)
	for _, verb := range []string{"links", "nocode", "quickstart"} {
		exit, _, stderr := runCheck(t, verb, "--dir", dir, "--max", "-1")
		assert.True(t, exit == 2 && strings.Contains(stderr, "--max must be zero or more"), "%s: exit = %d, stderr = %q", verb, exit, stderr)
	}
}

// A flag typo used to cost the whole 38-line banner.
func TestAFlagTypoIsOneLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		door string // the skeleton's door: a flag the verb does not take names the verb's -h, the rest name help
	}{
		{[]string{"links", "--diir", "x"}, "nova-check links -h"},
		{[]string{"nocode", "--diir", "x"}, "nova-check nocode -h"},
		{[]string{"frobnicate"}, "nova-check help"},
		{nil, "nova-check help"},
	} {
		args := tc.args
		exit, stdout, stderr := runCheck(t, args...)
		assert.EqualValues(t, 2, exit, "%v: exit = %d, want 2", args, exit)
		{
			got := countLines(stderr)
			assert.EqualValues(t, 1, got, "%v: the refusal is %d lines, want 1:\n%s", args, got, stderr)
		}
		assert.Contains(t, stderr, "run: "+tc.door, "%v: the refusal names no door: %q", args, stderr)
		assert.EqualValues(t, "", stdout, "%v: a refusal wrote to stdout: %q", args, stdout)
	}
	exit, stdout, _ := runCheck(t, "help")
	assert.True(t, exit == 0 && strings.Contains(stdout, "usage:"), "`help` did not print the usage: exit %d", exit)
}
