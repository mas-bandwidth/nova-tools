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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
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

// capWait is the fork-bomb tests' own deadline: NOVA_TEST_WAIT when set, thirty seconds
// otherwise. A wall that enforces its cap answers in about a second; this bounds a wall
// that does not, inside the package's alarm, so the failure is this test's and named.
func capWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// needProcCount skips BY NAME on a host where the wall cannot enforce its cap at all: the
// cap is a count of the group's live processes read from /proc, and no count is no cap.
func needProcCount(t *testing.T) {
	t.Helper()
	if u, err := sandbox.GroupUsage(syscall.Getpgrp()); err != nil || u.Procs == 0 {
		t.Skipf("skipped: the process cap counts the group in /proc and this host's /proc gives no count (%d processes, %v)", u.Procs, err)
	}
}

// endGroup sends SIGKILL to the group the test made for the tool, if any of it is still
// running. Rule 12 keeps the walled tree in that group, so it is the whole of what the
// test started; never a group the test did not make. It takes no t: the deadline calls
// it from exec's own goroutine.
func endGroup(pgid int) {
	if pgid > 1 {
		if u, err := sandbox.GroupUsage(pgid); err != nil || u.Procs == 0 {
			return
		}
		// ignored: the group may be gone between the count and the kill; the count after is the check
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// wallBomb runs script in the wall under the test's own deadline, with the tool started
// in a group of the test's making, the way a supervisor starts a job (rule 12: the tree
// stays in it). When the deadline fires the test ends what it started: that group.
// Whatever of the group is left when the test ends is ended too, so a failure here
// leaves nothing running. It answers the status, stderr, the group and whether the
// deadline fired; the pass condition is never the bomb's own end.
func (j job) wallBomb(t *testing.T, script string) (int, string, int, bool) {
	t.Helper()
	tool := walledTool(t) // built before the deadline starts: the build is not the run
	ctx, cancel := context.WithTimeout(context.Background(), capWait())
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", script)
	cmd.Env = j.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Cancel = func() error {
		endGroup(cmd.Process.Pid)
		return cmd.Process.Kill()
	}
	// The group's processes hold stderr; past the kill above, nothing the test started
	// does, so the copy is not waited on for long.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	pgid := 0
	if cmd.Process != nil {
		pgid = cmd.Process.Pid
		t.Cleanup(func() { endGroup(pgid) })
	}
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else {
		require.NoError(t, err, "nova-sandbox did not run at all: %v", err)
	}
	return code, errb.String(), pgid, ctx.Err() != nil
}

// assertCapped is the cap's effect, read after the wall has answered: the run ended with
// the runaway status and line, inside the test's deadline, and no process of the
// caller's group, which holds the whole tree, is running. The bombs below run sleep 60, so a run that ended in time did not end by
// itself.
func assertCapped(t *testing.T, code int, errOut string, pgid int, timedOut bool) {
	t.Helper()
	require.Positive(t, pgid, "the tool never ran: %s", errOut)
	require.False(t, timedOut, "the wall did not end the bomb inside the test's %s; the test ended group %d itself: %s", capWait(), pgid, errOut)
	assert.Regexp(t, `runaway: \d+ processes \(cap `+strconv.Itoa(sandbox.DefaultMaxProcs)+`\)`, errOut, "the run names the cap it passed")
	assert.Equal(t, sandbox.ExitRunaway, code, "the run ends with the runaway status: %s", errOut)
	assert.Empty(t, liveInGroup(t, pgid), "a process of the killed tree survives")
}

// TestWallCapsAForkBomb: a shell loop that forks until refused, itself capped at 1,000,
// runs under the wall; the wall kills its tree past the default cap of 256, and no
// process of the tree is left in the caller's group. Where RLIMIT_NPROC refuses a fork first, the shell exits
// and leaves its children, and that is the wall's to end as well.
func TestWallCapsAForkBomb(t *testing.T) {
	t.Parallel()
	needLandlock(t)
	needProcCount(t)
	j := newJob(t)
	code, errOut, pgid, timedOut := j.wallBomb(t, `i=0; while [ $i -lt 1000 ]; do sleep 60 & i=$((i+1)); done; wait`)
	assertCapped(t, code, errOut, pgid, timedOut)
}

// TestWallCapsATreeWhoseLeaderExits: the shape ubuntu-latest hosted ran into (run
// 37344601638). The leader forks past the cap and exits before the watch's first count;
// its children are reparented to the tool, a subreaper, so they are still the tree, past
// the cap, and the wall kills them.
func TestWallCapsATreeWhoseLeaderExits(t *testing.T) {
	t.Parallel()
	needLandlock(t)
	needProcCount(t)
	j := newJob(t)
	code, errOut, pgid, timedOut := j.wallBomb(t, `i=0; while [ $i -lt 300 ]; do sleep 60 & i=$((i+1)); done; exit 3`)
	assertCapped(t, code, errOut, pgid, timedOut)
}

// TestWallCapsLeaveANormalRunAlone: a run under the cap is not touched, and says nothing.
func TestWallCapsLeaveANormalRunAlone(t *testing.T) {
	t.Parallel()
	needLandlock(t)
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
