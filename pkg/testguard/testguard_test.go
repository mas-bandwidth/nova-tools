package testguard

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

func TestUnsetGuardLetsTheSeamRun(t *testing.T) {
	t.Parallel()
	g := NewGuard(false)
	require.False(t, g.refusing.Load(), "the guard must be off when the variable is unset; production pays nothing for it")
	g.RefuseHosts("ssh", "hulk", "uptime") // must not panic
}

func TestArmedGuardNamesTheCommandAndTheRemedy(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	defer func() {
		r := recover()
		require.NotNil(t, r, "an armed guard must refuse the seam")
		msg, _ := r.(string)
		for _, want := range []string{EnvNoHost, `"ssh"`, `"hulk"`, `"bash -s"`, "testguard.AllowHosts"} {
			assert.Contains(t, msg, want, "the refusal must carry %s; got %q", want, msg)
		}
	}()
	g.RefuseHosts("ssh", "hulk", "bash -s")
}

// TestAFakeOnPATHIsNotAHost is the half that keeps the honest test cheap: the
// repository's ssh fakes are scripts in t.TempDir() put on PATH, and a rule
// that made every one of them declare itself would be a rule people edit
// around. A program that resolves inside a temp directory is a fake; the fleet
// is never there.
func TestAFakeOnPATHIsNotAHost(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	dir := t.TempDir()
	fake := filepath.Join(dir, "ssh")
	if runtime.GOOS == "windows" {
		fake += ".bat"
	}
	require.NoError(t, testbin.WriteExecutable(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	g.lookPath = func(program string) (string, error) {
		if program == "ssh" {
			return fake, nil
		}
		return exec.LookPath(program)
	}
	g.RefuseHosts("ssh", "hulk", "uptime") // must not panic
	g.RefuseHosts(fake, "hulk", "uptime")  // named by absolute path, the same answer
}

func TestAllowHostsIsScopedAndNests(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	outer := g.AllowHosts()
	inner := g.AllowHosts()
	inner()
	g.RefuseHosts("ssh", "hulk") // the outer scope still stands
	inner()                      // closing twice is not a second decrement
	g.RefuseHosts("ssh", "hulk")
	outer()
	defer func() {
		require.NotNil(t, recover(), "the guard must be armed again once every scope has closed")
	}()
	g.RefuseHosts("ssh", "hulk")
}
