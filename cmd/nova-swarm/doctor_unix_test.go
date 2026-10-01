//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
	env, _, _ := doctorStubsGrace(t,
		"echo 'nova-swarm good-stamp'; sleep 5 & echo $! >> "+pidFile,
		"echo 'nova-swarm good-stamp'", "", doctorPipeGrace)
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

// A binary that prints its stamp and then hangs is read for the stamp and reported for the
// hang. The stub tells the test over a pipe that it has printed, and the test cancels the read
// then, so no wait pays the deadline and none depends on how fast the stub starts: the
// comparison and the refusal are those of a run that was cut off at its deadline. The stub
// runs once for the preflight and once for the doctor verb, and both are ended after both
// have printed. A stub that fails to start ends its run with an error, so the test fails on
// its assertions and closing the pipe at cleanup ends the goroutine that waits on it.
func TestPreflightComparesTheStampOfABinaryThatPrintsThenHangs(t *testing.T) {
	t.Parallel()
	signal := filepath.Join(t.TempDir(), "printed")
	if err := syscall.Mkfifo(signal, 0o600); err != nil {
		t.Fatal(err)
	}
	// Opened for reading and writing, the pipe never ends and a stub's write never blocks.
	pipe, err := os.OpenFile(signal, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipe.Close() })

	env, pathBin, _ := doctorStubs(t,
		"echo 'nova-swarm stale-stamp'; echo printed >> '"+signal+"'; exec sleep 30",
		"echo 'nova-swarm good-stamp'", "")
	readDefault := env.read

	var mu sync.Mutex
	var cancels []context.CancelFunc
	cancelAll := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
	}
	env.read = func(p string) (string, error) {
		if p != pathBin {
			return readDefault(p)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mu.Lock()
		cancels = append(cancels, cancel)
		mu.Unlock()
		return readVersionLineUnder(ctx, p, 2*time.Second, doctorGoodGrace, doctorVersionLineMax)
	}
	go func() {
		lines := bufio.NewReader(pipe)
		for i := 0; i < 2; i++ {
			if _, err := lines.ReadString('\n'); err != nil {
				break
			}
		}
		cancelAll()
	}()

	var out, derr, errOut bytes.Buffer
	var dcode int
	done := make(chan struct{})
	go func() {
		defer close(done)
		dcode = env.cmdDoctor(nil, &out, &derr)
	}()
	code, stop := env.preflight([]string{"native", "--tokens", "unmetered"}, &errOut)
	<-done
	if code != 2 || !stop {
		t.Fatalf("preflight(exit=%d, stop=%v), want (2, true)\n%s", code, stop, errOut.String())
	}
	got := errOut.String()
	for _, want := range []string{"DOCTOR DRIFT path=", "stale-stamp", "shadows", "DOCTOR UNREADABLE", "timed out after 2s"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if dcode != 2 || out.Len() != 0 || derr.String() != got {
		t.Errorf("doctor exit %d, want 2 with the preflight's finding\nstdout: %q\nstderr: %q\nwant stderr: %q", dcode, out.String(), derr.String(), got)
	}
}
