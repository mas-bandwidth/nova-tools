//go:build linux

// The wall's process and memory caps, docs/SPEC-SANDBOX.md "wall-caps-processes.w1". The
// fork bomb runs the real tool as a subprocess for the reason wall_linux_test.go gives: a
// Landlock domain cannot be lifted, so a Run() in the test binary would wall the binary.
package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveInGroup is the processes of group pgid that are still running. A zombie is a
// process that has died and not been collected, which is no survivor.
func liveInGroup(t *testing.T, pgid int) []int {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	require.NoError(t, err)
	var live []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(raw)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) > 2 && f[0] != "Z" && f[2] == strconv.Itoa(pgid) {
			live = append(live, pid)
		}
	}
	return live
}

// A cap test cannot prove enforcement without the wall and its process-count
// feature. Name the unavailable feature instead of running a meaningless bomb.
func needProcessCap(t *testing.T) {
	t.Helper()
	needLandlock(t)
	u, err := sandbox.GroupUsage(syscall.Getpgrp())
	if err != nil || u.Procs == 0 {
		t.Skipf("process cap needs readable procfs process statistics: count=%d error=%v", u.Procs, err)
	}
}

// wallUntil runs a wall with a caller-owned cancellation boundary. The separate
// helper lets the regression cancel on readiness instead of relying on machine speed.
func (j job) wallUntil(t *testing.T, ctx context.Context, script string) (int, string, string, error) {
	t.Helper()
	cmd := exec.CommandContext(ctx, walledTool(t), "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", script)
	cmd.Env = j.env()
	// The tool owns the wrapped child's process group and forwards SIGTERM to it.
	// Signal its os.Process (which tracks this child), never an unverified pid from
	// a file. WaitDelay bounds a broken tool or inherited stdout/stderr pipe.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return code, out.String(), errb.String(), err
}

// The control workload is finite even before the cancellation fix: one child
// exits after a minute. Cancellation must stop it before that natural completion.
func TestWallDeadlineStopsItsOwnChildren(t *testing.T) {
	t.Parallel()
	needProcessCap(t)
	j := newJob(t)
	pidFile := filepath.Join(j.write, "leader")
	completed := filepath.Join(j.write, "completed")
	_ = walledTool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan struct{})
	var runErr error
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		_, _, _, runErr = j.wallUntil(t, ctx, `echo $$ > `+pidFile+`; sleep 60 & wait; echo natural > `+completed)
		close(done)
	}()
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		_, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	cancel()
	<-done
	assert.ErrorIs(t, runErr, context.Canceled)
	assert.NoFileExists(t, completed, "the canceled command reached its natural end")
	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pgid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.Empty(t, liveInGroup(t, pgid), "cancellation left a child alive")
}

// TestWallCapsAForkBomb: a finite shell loop (300 children) runs with its own deadline,
// the run ends reporting runaway: <n> processes with n over the
// default cap of 256, and no process of the tree is left.
func TestWallCapsAForkBomb(t *testing.T) {
	t.Parallel()
	needProcessCap(t)
	j := newJob(t)
	pidFile := filepath.Join(j.write, "leader")
	script := `echo $$ > ` + pidFile + `; i=0; while [ $i -lt 300 ]; do sleep 60 & i=$((i+1)); done; wait`
	// Build before the deadline: compiling is unrelated to the command's runtime.
	_ = walledTool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, _, errOut, runErr := j.wallUntil(t, ctx, script)

	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err, "the leader never ran: %s", errOut)
	pgid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.Empty(t, liveInGroup(t, pgid), "a process of the killed tree survives")
	require.NotErrorIs(t, runErr, context.DeadlineExceeded, "the wall did not enforce its process cap before the test deadline: %s", errOut)

	m := regexp.MustCompile(`runaway: (\d+) processes`).FindStringSubmatch(errOut)
	require.NotNil(t, m, "no runaway line; the bomb was not bounded (exit %d): %s", code, errOut)
	n, _ := strconv.Atoi(m[1])
	t.Logf("watchdog observed %d processes; configured cap %d", n, sandbox.DefaultMaxProcs)
	assert.Greater(t, n, sandbox.DefaultMaxProcs, "the line names a count over the cap")
	assert.Equal(t, sandbox.ExitRunaway, code, "the run ends with the runaway status")

}

// TestWallCapsLeaveANormalRunAlone: a run under the cap is not touched, and says nothing.
func TestWallCapsLeaveANormalRunAlone(t *testing.T) {
	t.Parallel()
	needProcessCap(t)
	j := newJob(t)
	code, _, errOut := j.wall(t, `i=0; while [ $i -lt 20 ]; do sleep 0 & i=$((i+1)); done; wait; exit 7`)
	assert.Equal(t, 7, code, errOut)
	assert.NotContains(t, errOut, "runaway")
}

// TestPolicyOverNamesTheCapPast: the verdict is a function of the counts and the caps.
func TestPolicyOverNamesTheCapPast(t *testing.T) {
	t.Parallel()
	p := &sandbox.Policy{MaxProcs: 4, MaxMem: 100}
	_, hit := p.Over(sandbox.Usage{Procs: 4, RSS: 100})
	assert.False(t, hit, "at the cap is not past it")
	line, hit := p.Over(sandbox.Usage{Procs: 5})
	assert.True(t, hit)
	assert.Equal(t, "runaway: 5 processes (cap 4)", line)
	line, hit = p.Over(sandbox.Usage{Procs: 1, RSS: 101})
	assert.True(t, hit)
	assert.Equal(t, "runaway: 101 bytes of memory (cap 100)", line)
	_, hit = (&sandbox.Policy{}).Over(sandbox.Usage{Procs: 1 << 20, RSS: 1 << 50})
	assert.False(t, hit, "a policy built by hand with no caps is unbounded")
}
