//go:build functional

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A real process working inside a land clone, its argument line naming no path (`sleep`
// with its cwd in the clone), keeps the clone: the machine's own open-path reader (lsof on
// darwin, /proc on Linux) sees where it works, and the run says so. Once it has exited the
// same clone goes (Zhi's HOLD on #5136, 2026-10-02).
func TestDiskGuardKeepsALandCloneAProcessWorksIn(t *testing.T) {
	t.Parallel()
	land := t.TempDir()
	clone := filepath.Join(land, "github.com-o-r-0123456789abcdef")
	require.NoError(t, os.MkdirAll(filepath.Join(clone, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clone, ".git", "HEAD"), []byte("ref: refs/heads/land/s1\n"), 0o644))
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, filepath.WalkDir(clone, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	}))
	sleep := exec.Command("sleep", "60")
	sleep.Dir = clone
	require.NoError(t, sleep.Start())
	done := make(chan struct{})
	go func() { _ = sleep.Wait(); close(done) }() // ignored: the sleep is killed below; its exit is not under test

	guard := func() (*guard, *bytes.Buffer) {
		out := &bytes.Buffer{}
		return &guard{
			now: time.Now(), cloneAge: 24 * time.Hour, landDir: land, procs: processList, held: heldPaths,
			dirty: func(string) (bool, error) { return false, nil }, out: out,
		}, out
	}
	g, out := guard()
	g.landClones()
	assert.DirExists(t, clone, "a process working in the clone keeps it")
	assert.Contains(t, out.String(), "KEPT land clone "+clone+": a live process holds ")
	assert.Zero(t, g.failed, out.String())

	require.NoError(t, sleep.Process.Kill())
	<-done
	g, out = guard()
	g.landClones()
	assert.NoDirExists(t, clone, "with the process gone the clone goes: %s", out.String())
}

// The scratch rule's git check on real repositories: a clone whose commits its remote has
// holds no work; the same clone with a commit of its own, or a change to a tracked file,
// holds work; an untracked file alone does not. The check never rewrites the index, so it
// never makes an entry look used.
func TestScratchGitWorkOnRealRepositories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	git := func(at string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", at, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	origin := filepath.Join(dir, "origin.git")
	require.NoError(t, exec.Command("git", "init", "-q", "--bare", origin).Run())
	clone := filepath.Join(dir, "clone")
	require.NoError(t, exec.Command("git", "clone", "-q", origin, clone).Run())
	require.NoError(t, os.WriteFile(filepath.Join(clone, "a"), []byte("a\n"), 0o644))
	git(clone, "add", "a")
	git(clone, "commit", "-q", "-m", "a")
	git(clone, "push", "-q", "origin", "HEAD")
	assert.Equal(t, "", scratchGitWork(clone))

	require.NoError(t, os.WriteFile(filepath.Join(clone, "untracked"), []byte("u\n"), 0o644))
	assert.Equal(t, "", scratchGitWork(clone), "an untracked file alone is no work")

	index := filepath.Join(clone, ".git", "index")
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(index, old, old))
	require.NoError(t, os.WriteFile(filepath.Join(clone, "a"), []byte("changed\n"), 0o644))
	assert.Contains(t, scratchGitWork(clone), "has uncommitted work")
	fi, err := os.Stat(index)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(old), "the check never rewrites the index")

	git(clone, "commit", "-q", "-am", "local")
	assert.Contains(t, scratchGitWork(clone), "has commits no remote has")
}
