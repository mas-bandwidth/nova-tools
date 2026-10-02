package bus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/stretchr/testify/require"
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
	if fake := os.Getenv("NOVA_BUS_TIMEOUT_TEST_CHILD"); fake != "" {
		checkGitTimeoutInChild(t, fake)
		return
	}
	fake := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n"
	require.NoError(t, testbin.WriteExecutable(filepath.Join(fake, "git"), []byte(script), 0o755))

	// The child is a fresh test process, so its positive SetGitTimeout calls cannot
	// alter another test's budget. The fake git is selected through only this child's
	// environment, without changing the parent process PATH.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAGitThatHangsIsKilledAndNamed$")
	cmd.WaitDelay = 2 * time.Second
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "NOVA_BUS_TIMEOUT_TEST_CHILD=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"), "NOVA_BUS_TIMEOUT_TEST_CHILD="+fake)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "bounded timeout child failed: %s", out)
}

func checkGitTimeoutInChild(t *testing.T, fake string) {
	t.Helper()
	require.NoError(t, SetGitTimeout(300*time.Millisecond))

	// The assertion is on the refusal and not on elapsed time: a wall-clock bound here
	// measures the machine's load, and the injected subprocess budget is what is under
	// test. The refusal firing on the deadline is what says the call was cut short.
	_, err := git(t.TempDir(), "fetch", "origin", "main")
	require.Error(t, err, "a git that never returns was waited on forever and reported success")
	// The refusal names the CALL, which is the one thing a person needs in order to know
	// what was hanging.
	for _, want := range []string{"fetch origin main", "did not finish within", "300ms", "--git-timeout"} {
		require.Contains(t, err.Error(), want, "the refusal does not say %q: %v", want, err)
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
	require.NoError(t, SetGitTimeout(30*time.Second))
	quick := "#!/bin/sh\necho fine\n"
	require.NoError(t, testbin.WriteExecutable(filepath.Join(fake, "git"), []byte(quick), 0o755))
	out, err := git(t.TempDir(), "fetch", "origin", "main")
	require.NoError(t, err, "a git well inside the budget was refused: %v", err)
	require.Equal(t, "fine", strings.TrimSpace(out), "git printed %q", out)
	// And a budget of nothing is a bad invocation rather than a call with no budget at all.
	{
		err := SetGitTimeout(0)
		require.Error(t, err, "a budget of zero was accepted; every call would be killed before it started")
	}
}
