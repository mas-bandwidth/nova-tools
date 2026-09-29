//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// reapRecordedChildren ends every process a stub recorded in pidFile (one pid per line, one
// line per run of the stub) and fails the test if any of them is still alive afterwards. The
// stub's children are orphans, so they cannot be waited for; a process that is gone answers a
// signal-0 with ESRCH, and the check spins on that under a bound rather than sleeping.
func reapRecordedChildren(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Errorf("the stub recorded no child pid: %v", err)
		return
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 1 {
			t.Errorf("the pid file holds %q, want one pid per line", field)
			continue
		}
		pids = append(pids, pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pid := range pids {
		for syscall.Kill(pid, 0) == nil && ctx.Err() == nil {
			runtime.Gosched()
		}
		if syscall.Kill(pid, 0) == nil {
			t.Errorf("the stub's background child %d is still alive after it was killed", pid)
		}
	}
}

// A binary that prints its stamp, exits 0 and leaves a background child holding the output
// pipe has answered: the wait for the pipe gives up after the grace and the stamp is read.
// The stub runs once for the preflight and once for the doctor verb, and each run appends
// its child's pid, so the cleanup ends every child, not the last; the child's own sleep is
// short, so a test process that dies before its cleanup leaves nothing for long.
func TestPreflightReadsABinaryWhoseChildHoldsThePipe(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "children.pid")
	env, _, _ := doctorStubs(t,
		"echo 'nova-swarm good-stamp'; sleep 5 & echo $! >> "+pidFile,
		"echo 'nova-swarm good-stamp'", "", 0)
	t.Cleanup(func() { reapRecordedChildren(t, pidFile) })
	var errOut bytes.Buffer
	if code, stop := env.preflight([]string{"native", "--card", "c"}, &errOut); stop || code != 0 {
		t.Fatalf("preflight(exit=%d, stop=%v), want (0, false)\n%s", code, stop, errOut.String())
	}
	var out, derr bytes.Buffer
	if code := env.cmdDoctor(nil, &out, &derr); code != 0 || out.String() != "DOCTOR OK stamp=nova-swarm good-stamp\n" {
		t.Errorf("doctor: exit %d, stdout %q, stderr %q", code, out.String(), derr.String())
	}
	if raw, err := os.ReadFile(pidFile); err != nil || len(strings.Fields(string(raw))) != 2 {
		t.Errorf("the stub ran twice and should have recorded two children, got %q (%v)", raw, err)
	}
}
