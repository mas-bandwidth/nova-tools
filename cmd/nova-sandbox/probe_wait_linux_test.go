//go:build linux

package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A valid probe must use a dedicated process, even when a test only checks that
// --secret is optional. WNOWAIT holds another command's zombie until after the
// probe: moving probeTool back to the in-process tool steals this wait every time.
func TestAValidProbePreservesTheCallersCommandWait(t *testing.T) {
	t.Parallel()
	needLandlock(t)
	if os.Geteuid() == 0 {
		t.Skip("the wall refuses root; this witness requires a valid probe")
	}
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() }) // ignored: the assertion owns the first wait
	var info unix.Siginfo
	require.NoError(t, unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil))
	j := newJob(t)
	code, out, errOut := j.probeTool(t, j.env(), "--write", j.write)
	require.Equal(t, 0, code, "the real probe must succeed: %s; %s", out, errOut)
	require.Contains(t, out, "PROBE OK", "the valid probe must run its walled steps")
	require.NoError(t, cmd.Wait(), "a probe in the shared test process stole another command's wait")
}
