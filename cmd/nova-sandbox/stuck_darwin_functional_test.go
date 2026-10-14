//go:build functional && darwin

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStuckReadsAWalledLaunchThisTestStartsAndEnds: the unit tests step the verb
// on recorded text; this one runs it against a live launch, the way a person or
// the seat does on a host. The test starts a walled `sleep 1000`, waits until
// the launch is old enough to be a candidate, reads it with `--min-age 1s`, and
// ends the tree itself: the verb writes nothing, kills nothing and starts
// nothing, so the ending is the test's own, of a process this test started.
// It is behind the functional tag because it waits on the real wall and the
// real process table, and no injected clock can make a live launch age.
func TestStuckReadsAWalledLaunchThisTestStartsAndEnds(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	bin := toolBinary(t)
	wrapped := exec.Command(bin, "--read", j.read, "--write", j.write, "--", "/bin/sleep", "1000")
	wrapped.Env = j.env()
	wrapped.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, wrapped.Start(), "the walled sleep did not start")
	pgid := wrapped.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })

	// The candidate is the wrapper's own child, and --min-age 1s selects a
	// launch at least a second old: poll the same table the verb reads until
	// the sleep is there and of age.
	child := 0
	for i := 0; i < 300 && child == 0; i++ {
		ps, err := psText()
		require.NoError(t, err, "the process table did not read: %v", err)
		for _, r := range parsePS(ps) {
			if r.PPID == pgid && commandBase(r.Command) == "sleep" && r.ETime >= time.Second {
				child = r.PID
			}
		}
		if child == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	require.NotZero(t, child, "the walled sleep never appeared under the wrapper within 30s of ps reads")

	diag := exec.Command(bin, "stuck", "--pid", strconv.Itoa(child), "--min-age", "1s")
	raw, err := diag.CombinedOutput()
	require.NoError(t, err, "the stuck verb did not run at exit 0: %s", raw)
	out := string(raw)
	assert.Contains(t, out, fmt.Sprintf("PS pid=%d ppid=%d etime=", child, pgid), "the launch's ps row is missing:\n%s", out)
	assert.Contains(t, out, "STUCK HOST productVersion=", "the host's productVersion is not printed beside the verdict:\n%s", out)
	assert.Regexp(t, `STUCK LAUNCH pid=`+strconv.Itoa(child)+` state=\S+ cpu=\S+ age=\S+ top=\S* denied=none`, out, "the verdict line does not read the launch (a plain sleep was denied nothing):\n%s", out)

	// The ending is the test's: the tree this test started is ended by it.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	_ = wrapped.Wait()
}
