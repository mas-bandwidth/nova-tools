package bus

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// A git that never returns is a tool that has stopped saying anything, which from the
// outside is indistinguishable from a tool that is working. Every subprocess runs under a
// budget, and a call that overruns it is killed and named.
//
// The git here is a script that sleeps, so the assertion is about the budget and not about
// a network nobody has.
func TestAGitThatHangsIsKilledAndNamed(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("slow: kills a hanging git behind a real budget; runs on the self-hosted legs and nightly")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	fake := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n"
	fakeGit := filepath.Join(fake, "git")
	if err := testbin.WriteExecutable(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// The assertion is on the refusal and not on elapsed time: a wall-clock bound here
	// measures the machine's load, and the injected subprocess budget is what is under
	// test. The refusal firing on the deadline is what says the call was cut short.
	_, err := gitWith(gitSeams{gitBin: fakeGit, timeout: 300 * time.Millisecond}, t.TempDir(), "fetch", "origin", "main")
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

	// The other way: a budget that is not exceeded is not a refusal. A git that answers
	// quickly answers.
	quick := "#!/bin/sh\necho fine\n"
	if err := testbin.WriteExecutable(fakeGit, []byte(quick), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := gitWith(gitSeams{gitBin: fakeGit, timeout: 30 * time.Second}, t.TempDir(), "fetch", "origin", "main")
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
