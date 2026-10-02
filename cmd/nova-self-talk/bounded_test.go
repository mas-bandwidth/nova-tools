package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

// largePage builds the state the audit measured this binary at: six hundred standing
// claims and six hundred dated ones in one file — 1,201 lines and ~78K tokens, of which
// half was the tool congratulating the writer, at length, on the half they got right.
func largePage(t *testing.T, standing, dated int) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < standing; i++ {
		fmt.Fprintf(&b, "I cannot check my own work on the %dth pass, so the second read went to someone else.\n\n", i)
	}
	for i := 0; i < dated; i++ {
		fmt.Fprintf(&b, "On 2026-08-%02d I cannot check the Windows runner from here, and the fix went in with that measurement written beside it.\n\n", i%28+1)
	}
	path := filepath.Join(t.TempDir(), "journal.md")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	return path
}

func TestScanCapsFindingsAndCountsTheDated(t *testing.T) {
	t.Parallel()

	page := largePage(t, 600, 600)
	exit, stdout, stderr := runSelfTalk(t, page)
	require.Equal(t, 1, exit, "exit = %d, want 1; stderr: %s", exit, stderr)
	got := countLines(stderr)
	assert.Equal(t, bounded.Default+1, got, "stderr is %d lines, want %d findings + one MORE line", got, bounded.Default)
	assert.Contains(t, stderr, "SELFTALK MORE kind=standing shown=20 total=600", "no MORE line naming the total:\n%s", stderr)
	// THE WELCOME CASE IS A COUNT. Six hundred dated claims are one line.
	assert.Contains(t, stdout, "SELFTALK DATED n=600 files=1", "the dated claims are not counted: %s", stdout)
	assert.NotContains(t, stdout, "SELFTALK DATED "+page, "a dated claim is still quoted")
	assert.Contains(t, stdout, "standing=600", "no count line on failure: %s", stdout)
	// stdout: the DATED count, the FAIL count, the NOTE.
	got = countLines(stdout)
	assert.Equal(t, 3, got, "stdout is %d lines, want 3 (dated count, count line, NOTE):\n%s", got, stdout)
}

// The two classes are capped separately, for the same reason the classes exist: the
// INSTALLATION finding is the one the first class cannot see, and a flat cap over six
// hundred STANDING claims would eat it.
func TestScanCapsEachClassSeparately(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "I cannot check my own work on the %dth pass, so the second read went to someone else.\n\n", i)
	}
	b.WriteString("It is the worst habit I have, and the reason the checklist exists at all.\n")
	path := filepath.Join(t.TempDir(), "journal.md")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	_, _, stderr := runSelfTalk(t, path)
	require.Contains(t, stderr, "INSTALLATION", "the buried class never printed:\n%s", stderr)
	assert.Contains(t, stderr, "SELFTALK MORE kind=standing", "the loud class was not capped:\n%s", stderr)
	assert.NotContains(t, stderr, "SELFTALK MORE kind=installation", "the quiet class was capped though it had one finding:\n%s", stderr)
}

func TestMaxWidensAndZeroPrintsAll(t *testing.T) {
	t.Parallel()

	page := largePage(t, 600, 0)
	_, _, stderr := runSelfTalk(t, "--max", "5", page)
	got := countLines(stderr)
	assert.Equal(t, 6, got, "--max 5 gave %d lines, want 5 + MORE", got)
	_, _, stderr = runSelfTalk(t, "--max", "0", page)
	got = countLines(stderr)
	assert.Equal(t, 600, got, "--max 0 gave %d lines, want all 600", got)
	assert.NotContains(t, stderr, "SELFTALK MORE", "--max 0 elided nothing and must print no MORE line")
}

func TestRefusesANegativeCeiling(t *testing.T) {
	t.Parallel()

	page := largePage(t, 2, 0)
	exit, _, stderr := runSelfTalk(t, "--max", "-1", page)
	assert.Equal(t, 2, exit, "exit = %d, stderr = %q", exit, stderr)
	assert.Contains(t, stderr, "--max must be a line ceiling", "exit = %d, stderr = %q", exit, stderr)
}

// A flag typo used to cost the whole 40-line banner. It costs one line, plus the one
// hint line this repo's guidance law requires, and it names the door.
func TestARefusalIsAtMostTwoLinesAndNamesTheDoor(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--skipp", "x", "a.md"},
		nil,
	} {
		exit, stdout, stderr := runSelfTalk(t, args...)
		assert.Equal(t, 2, exit, "%v: exit = %d, want 2", args, exit)
		got := countLines(stderr)
		assert.LessOrEqual(t, got, 2, "%v: the refusal is %d lines, want at most 2:\n%s", args, got, stderr)
		assert.Contains(t, stderr, "run: nova-self-talk help", "%v: the refusal names no door: %q", args, stderr)
		assert.Empty(t, stdout, "%v: a refusal wrote to stdout: %q", args, stdout)
	}
	exit, stdout, _ := runSelfTalk(t, "help")
	assert.Equal(t, 0, exit, "`help` did not print the usage: exit %d", exit)
	assert.Contains(t, stdout, "usage:", "`help` did not print the usage: exit %d", exit)
}
