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
