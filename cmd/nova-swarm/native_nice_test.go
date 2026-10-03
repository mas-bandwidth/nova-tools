//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE DARWIN WALL FORBIDS setpriority (its `system-sched` operation, measured 2026-10-02:
// under `(allow default)(deny system-sched)` `nice -n 19 true` prints `nice: setpriority:
// Operation not permitted`, and the wall's template is `(deny default)` with no
// system-sched grant). Every card's gate line begins `nice -n 19`, so every worker's output
// carried the warning and its gate ran at the priority it started with. Native now lowers
// the walled child's whole group itself, outside the wall, and puts a `nice` in the shim
// directory that runs the command without asking the wall for what the group already has.

// nativeNicesChild is the one decision: the darwin wall, and only it.
func TestNativeNicesTheChildWhereTheWallForbidsIt(t *testing.T) {
	t.Parallel()
	assert.True(t, nativeNicesChild("darwin", true), "the darwin wall forbids setpriority: native lowers the child")
	assert.False(t, nativeNicesChild("darwin", false), "an unwalled child nices itself")
	assert.False(t, nativeNicesChild("linux", true), "landlock leaves setpriority alone: the card's own nice works")
	tmpl, err := os.ReadFile(filepath.Join("..", "..", "profiles", "darwin.sb.tmpl"))
	require.NoError(t, err)
	assert.Contains(t, string(tmpl), "(deny default)", "the premise: the darwin wall denies what it does not grant")
	assert.NotContains(t, string(tmpl), "system-sched", "the premise: the darwin wall grants no system-sched")
}

// lowerChildPriority puts a running group at nice 19, as native does to the walled harness
// right after it starts.
func TestLowerChildPriorityPutsTheGroupAtNineteen(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/sh", "-c", "read x")
	ownChildGroup(cmd)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	defer func() {
		_ = stdin.Close() // ignored: the child reads EOF and exits; its exit is not under test
		_ = cmd.Wait()    // ignored: as above
	}()
	require.NoError(t, lowerChildPriority(cmd.Process.Pid))
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, cmd.Process.Pid)
	require.NoError(t, err)
	nice := raw
	if runtime.GOOS == "linux" {
		nice = 20 - raw // the raw syscall answers 20-nice on linux
	}
	assert.Equal(t, 19, nice, "the child's group runs at nice 19")
}

// Inside a profile that denies system-sched as the wall does, a card's `nice -n 19 <cmd>`
// resolved through the shim directory runs the command and says nothing; the same line
// without the shim is the warning every worker printed.
func TestTheNiceShimRunsTheCommandWithoutTheWallsWarning(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the darwin wall is the one that forbids setpriority")
	}
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	dir := t.TempDir()
	require.NoError(t, writeNativeNiceShim(dir))
	walled := func(path, script string) (string, string) {
		cmd := exec.Command(sandboxExec, "-p", "(version 1)(allow default)(deny system-sched)", "/bin/sh", "-c", script)
		cmd.Env = []string{"PATH=" + path}
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		require.NoError(t, cmd.Run(), "stderr: %s", errb.String())
		return out.String(), errb.String()
	}
	gate := `nice -n 19 /bin/sh -c 'echo ran'`
	out, stderr := walled("/usr/bin:/bin", gate)
	require.Equal(t, "ran\n", out)
	require.Contains(t, stderr, "setpriority", "the premise: the profile denies what the wall denies")
	out, stderr = walled(dir+":/usr/bin:/bin", gate+` && nice -19 echo short && nice --adjustment=5 -- echo long && nice`)
	assert.Equal(t, "ran\nshort\nlong\n19\n", out)
	assert.Empty(t, strings.TrimSpace(stderr), "the shim's nice says nothing inside the wall")
}
