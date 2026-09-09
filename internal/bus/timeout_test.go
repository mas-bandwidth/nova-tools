package bus

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A git that never returns is a tool that has stopped saying anything, which from the
// outside is indistinguishable from a tool that is working. Every subprocess runs under a
// budget, and a call that overruns it is killed and named.
//
// The git here is a script that sleeps, so the assertion is about the budget and not about
// a network nobody has.
func TestAGitThatHangsIsKilledAndNamed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	fake := t.TempDir()
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(fake, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))

	restore := gitTimeoutNanos.Load()
	t.Cleanup(func() { gitTimeoutNanos.Store(restore) })
	if err := SetGitTimeout(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := git(t.TempDir(), "fetch", "origin", "main")
	took := time.Since(start)
	if err == nil {
		t.Fatal("a git that never returns was waited on forever and reported success")
	}
	// The refusal names the CALL, which is the one thing a person needs in order to know
	// what was hanging.
	for _, want := range []string{"fetch origin main", "did not finish within", "--git-timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	if took > 10*time.Second {
		t.Fatalf("the call took %s under a 300ms budget; it was not killed", took)
	}

	// The other way: a budget that is not exceeded is not a refusal. A git that answers
	// quickly answers.
	//
	// On a budget of its own, and a generous one, because THIS half is not about the
	// deadline. Starting a subprocess out of this test binary is not free -- a fork of a
	// race-instrumented process is a fork of everything it has mapped -- and under
	// `-race -count=N` that alone has overrun 300ms here, which failed the test with the
	// refusal that the OTHER half exists to prove happens. A wall-clock margin that has to
	// hold for a fork is a wall clock in a test, so it is made wide enough not to be one.
	if err := SetGitTimeout(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	quick := "#!/bin/sh\necho fine\n"
	if err := os.WriteFile(filepath.Join(fake, "git"), []byte(quick), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := git(t.TempDir(), "fetch", "origin", "main")
	if err != nil {
		t.Fatalf("a git well inside the budget was refused: %v", err)
	}
	if strings.TrimSpace(out) != "fine" {
		t.Fatalf("git printed %q", out)
	}
	// And a budget of nothing is a bad invocation rather than a call with no budget at all.
	if err := SetGitTimeout(0); err == nil {
		t.Fatal("a budget of zero was accepted; every call would be killed before it started")
	}
}
