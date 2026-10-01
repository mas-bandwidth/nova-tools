package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

// crowdedBox quarantines n surfaces through the tool's own verb, so the box under test is
// one this binary actually wrote rather than one a test invented.
func crowdedBox(t *testing.T, n int) string {
	t.Helper()
	box := filepath.Join(t.TempDir(), "fuse-box.json")
	exit, _, stderr := runFuse(t, "init", "--box", box)
	require.Equal(t, 0, exit, "init: exit %d; stderr: %s", exit, stderr)
	for i := 0; i < n; i++ {
		exit, _, stderr := runFuse(t, "quarantine", "--box", box,
			fmt.Sprintf("a-surface-%03d", i), "a post addressed me and asked for a token")
		require.Equal(t, 0, exit, "quarantine %d: exit %d; stderr: %s", i, exit, stderr)
	}
	return box
}

func TestStatusMaxWidensAndZeroListsAll(t *testing.T) {
	t.Parallel()

	box := crowdedBox(t, 60)
	_, stdout, _ := runFuse(t, "status", "--box", box, "--max", "5")
	got := countLines(stdout)
	assert.Equal(t, 7, got, "--max 5 gave %d lines, want count + 5 + MORE", got)
	_, stdout, _ = runFuse(t, "status", "--box", box, "--max", "0")
	got = countLines(stdout)
	assert.Equal(t, 61, got, "--max 0 gave %d lines, want count + all 60", got)
	assert.NotContains(t, stdout, "STATUS MORE", "--max 0 elided nothing and must print no MORE line")
}

// A box with few enough surfaces to list is unchanged: no MORE line, and the same lines
// as before this cap existed.
func TestStatusUnderTheCeilingIsUnchanged(t *testing.T) {
	t.Parallel()

	box := crowdedBox(t, 3)
	_, stdout, _ := runFuse(t, "status", "--box", box)
	got := countLines(stdout)
	assert.Equal(t, 4, got, "stdout is %d lines, want count + 3:\n%s", got, stdout)
	assert.NotContains(t, stdout, "STATUS MORE", "a three-surface box printed a MORE line:\n%s", stdout)
}

func TestStatusRefusesANegativeCeiling(t *testing.T) {
	t.Parallel()

	box := crowdedBox(t, 1)
	exit, _, stderr := runFuse(t, "status", "--box", box, "--max", "-1")
	assert.Equal(t, 2, exit, "exit = %d, stderr = %q", exit, stderr)
	assert.Contains(t, stderr, "--max must be a line ceiling")
}

// A flag typo used to cost the whole 32-line banner, and a surface name beginning with a
// dash is the realistic shape here.
func TestARefusalIsOneLineAndNamesTheDoor(t *testing.T) {
	t.Parallel()

	box := crowdedBox(t, 1)
	for _, args := range [][]string{
		{"status", "--boxx", box},
		{"defuse"},
		{"lockdown", "--box", box},
		{"quarantine", "--box", box},
		{"check", "--box", box, "a", "b"},
		{"path", "--box", box, "extra"},
		{"lift"},
		{"lift", "sideways"},
		nil,
	} {
		exit, stdout, stderr := runFuse(t, args...)
		assert.Equal(t, 2, exit, "%v: exit = %d, want 2", args, exit)
		got := countLines(stderr)
		assert.Equal(t, 1, got, "%v: the refusal is %d lines, want 1:\n%s", args, got, stderr)
		assert.Contains(t, stderr, "run: nova-fuse help", "%v: the refusal names no door: %q", args, stderr)
		assert.Empty(t, stdout, "%v: a refusal wrote to stdout: %q", args, stdout)
	}
	// `lift lockdown` is the one refusal here that is NOT one line, and it is meant to
	// be read rather than scanned. It must stay that way.
	exit, _, stderr := runFuse(t, "lift", "lockdown")
	assert.Equal(t, 2, exit, "the hard refusal changed shape: exit %d, %q", exit, stderr)
	assert.Contains(t, stderr, "REFUSED, forever, by design", "the hard refusal changed shape: exit %d, %q", exit, stderr)
	exit, stdout, _ := runFuse(t, "help")
	assert.Equal(t, 0, exit, "`help` did not print the usage: exit %d", exit)
	assert.Contains(t, stdout, "usage:", "`help` did not print the usage: exit %d", exit)
}
