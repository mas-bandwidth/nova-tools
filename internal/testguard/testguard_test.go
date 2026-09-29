package testguard

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

func TestUnsetGuardLetsTheSeamRun(t *testing.T) {
	t.Parallel()
	g := NewGuard(false)
	if g.Refusing() {
		t.Fatal("the guard must be off when the variable is unset; production pays nothing for it")
	}
	g.RefuseHosts("ssh", "hulk", "uptime") // must not panic
}

func TestArmedGuardNamesTheCommandAndTheRemedy(t *testing.T) {
	t.Parallel()
	g := NewGuard(true)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("an armed guard must refuse the seam")
		}
		msg, _ := r.(string)
		for _, want := range []string{EnvNoHost, `"ssh"`, `"hulk"`, `"bash -s"`, "testguard.AllowHosts"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal must carry %s; got %q", want, msg)
			}
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
	if err := testbin.WriteExecutable(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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
		if recover() == nil {
			t.Fatal("the guard must be armed again once every scope has closed")
		}
	}()
	g.RefuseHosts("ssh", "hulk")
}
