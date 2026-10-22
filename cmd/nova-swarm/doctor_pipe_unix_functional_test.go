//go:build !windows && functional

// A binary whose background child holds the output pipe is read by waiting the pipe's grace
// out on the clock, with a real stub and its real child: functional by the house rule
// (docs/TESTING.md, "The two tiers"). The doctor's other readings stay in the unit tier
// (doctor_test.go, doctor_unix_test.go), where a hung stub is cut off at an injected 10ms.

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doctorPipeGrace is the grace for the one stub that leaves a child holding the output
// pipe, which the reader must wait out.
const doctorPipeGrace = 250 * time.Millisecond

// reapRecordedChildren ends every process a stub recorded in pidFile (one pid per line, one
// line per run of the stub) and fails the test if any of them is still alive afterwards. The
// stub's children are orphans, so they cannot be waited for; a process that is gone answers a
// signal-0 with ESRCH, and the check spins on that under a bound rather than sleeping.
func reapRecordedChildren(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if !assert.NoError(t, err, "the stub recorded no child pid: %v", err) {
		return
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if !assert.NoError(t, err, "the pid file holds %q, want one pid per line", field) || !assert.Greater(t, pid, 1, "the pid file holds %q, want one pid per line", field) {
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
		assert.Error(t, syscall.Kill(pid, 0), "the stub's background child %d is still alive after it was killed", pid)
	}
}

// A binary that prints its stamp, exits 0 and leaves a background child holding the output
// pipe has answered: the wait for the pipe gives up after the grace and the stamp is read.
// The stub runs once for the preflight and once for the doctor verb, and each run appends
// its child's pid, so the cleanup ends every child, not the last; the child's own sleep is
// short, so a test process that dies before its cleanup leaves nothing for long. The two
// reads run at the same time, as in TestPreflightRefusesAnUnreadableBinary, so the grace is
// waited out once.
func TestPreflightReadsABinaryWhoseChildHoldsThePipe(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "children.pid")
	env, _, _ := doctorStubsGrace(t,
		"echo 'nova-swarm good-stamp'; sleep 5 & echo $! >> "+pidFile,
		"echo 'nova-swarm good-stamp'", "", doctorPipeGrace)
	t.Cleanup(func() { reapRecordedChildren(t, pidFile) })
	var out, derr, errOut bytes.Buffer
	var dcode int
	done := make(chan struct{})
	go func() {
		defer close(done)
		dcode = env.cmdDoctor(nil, &out, &derr)
	}()
	code, stop := env.preflight([]string{"native", "--card", "c"}, &errOut)
	<-done
	require.False(t, stop, "preflight(exit=%d, stop=%v), want (0, false)\n%s", code, stop, errOut.String())
	require.Equal(t, 0, code, "preflight(exit=%d, stop=%v), want (0, false)\n%s", code, stop, errOut.String())
	code = dcode
	assert.Equal(t, 0, code, "doctor: exit %d, stdout %q, stderr %q", code, out.String(), derr.String())
	assert.Equal(t, "DOCTOR OK stamp=nova-swarm good-stamp\n", out.String(), "doctor: exit %d, stdout %q, stderr %q", code, out.String(), derr.String())
	raw, err := os.ReadFile(pidFile)
	assert.NoError(t, err, "the stub ran twice and should have recorded two children, got %q (%v)", raw, err)
	assert.Len(t, strings.Fields(string(raw)), 2, "the stub ran twice and should have recorded two children, got %q (%v)", raw, err)
}
