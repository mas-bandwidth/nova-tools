package bench

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The standalone check of a staged bench tree (carried from nova-sprint 3c3d49e6): a .git
// that is a file pointing at a worktree's gitdir is refused with what it points at, and
// WriteTree refuses to copy such a .git. The remote lines run under a local sh in a
// temporary directory: no host, no network.

// runLine runs a remote line with sh, as the bench's ssh shell does.
func runLine(t *testing.T, line string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	cmd := exec.Command("sh", "-c", line)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), ee.ExitCode()
	}
	require.NoError(t, err)
	return out.String(), 0
}

func TestStandaloneLineAcceptsACloneDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	out, code := runLine(t, StandaloneLine(dir))
	assert.Zero(t, code, "a tree with a .git directory:\n%s", out)
}

func TestStandaloneLineRefusesAWorktreePointerAtAPathTheHostLacks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	gone := filepath.Join(root, "clone", ".git", "worktrees", "x") // never created
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gone+"\n"), 0o644))
	out, code := runLine(t, StandaloneLine(dir))
	require.Equal(t, NotStandalone, code, out)
	for _, want := range []string{"is not standalone", gone, "does not exist on this host", "clone made on the bench"} {
		assert.Contains(t, out, want, "the refusal says %q", want)
	}
}

func TestStandaloneLineRefusesAWorktreePointerAtAPathThatExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	other := filepath.Join(root, "mirror", "worktrees", "x")
	for _, d := range []string{dir, other} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+other+"\n"), 0o644))
	out, code := runLine(t, StandaloneLine(dir))
	assert.Equal(t, NotStandalone, code, out)
	assert.NotContains(t, out, "does not exist", "a worktree pointer at an existing path")
}

func TestStandaloneLineRefusesATreeWithNoGit(t *testing.T) {
	t.Parallel()
	out, code := runLine(t, StandaloneLine(t.TempDir()))
	assert.Equal(t, NotStandalone, code, out)
	assert.Contains(t, out, "has no .git directory")
}

func TestStageLineEndsWithTheStandaloneCheck(t *testing.T) {
	t.Parallel()
	s := MirrorStage{Mirror: "nova-bench/mirror/x.git", Remote: "https://example.invalid/x.git", Ref: GateRef("s", strings.Repeat("a", 40)), Sha: strings.Repeat("a", 40)}
	line := StageLine(s, "nova-bench/runs/r/repo")
	assert.True(t, strings.HasSuffix(line, StandaloneLine("nova-bench/runs/r/repo")+"; }"), "the stage line does not end with the standalone check:\n%s", line)
}

func TestWriteTreeRefusesAWorktreeGitFileWithGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent/x/.git/worktrees/y\n"), 0o644))
	var b bytes.Buffer
	err := WriteTree(&b, dir, true)
	require.Error(t, err, "WriteTree with the .git of a worktree")
	assert.Contains(t, err.Error(), "clone made on the bench")
	b.Reset()
	assert.NoError(t, WriteTree(&b, dir, false), "a worktree's .git left out is fine")
}
