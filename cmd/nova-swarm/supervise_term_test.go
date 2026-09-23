//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ISSUE #1598: tests leaked `nova-swarm-swarmtest supervise` processes that ignored TERM
// (found 21–38 hours old on a CI runner). A supervise started by a test must not outlive
// that test, must end on TERM, and must end when its pool root is gone.

// TestALeftoverSuperviseIsReaped is the leak shape: the runner dies after spawn and the
// supervisor stops before identify, which is a process that ignores TERM until someone
// SIGCONTs it. The subtest returns without noting the pid. Cleanup must still reap it;
// a poll here that finds it alive is the leak.
func TestALeftoverSuperviseIsReaped(t *testing.T) {
	var pid int
	t.Run("spawn", func(t *testing.T) {
		b := newBench(t)
		pid = orphanStoppedSupervise(t, b)
	})
	if pid <= 0 {
		t.Fatal("the spawn subtest never recorded a supervisor pid")
	}
	t.Cleanup(func() { reapSupervise(pid, 0) })
	waitGoneOrFail(t, pid, "a stopped supervise the subtest forked and did not note")
}

// TestSuperviseEndsOnTERM: SIGTERM ends the supervisor AND the harness it released into
// its own process group. Default Go death of the supervisor alone leaves the harness.
func TestSuperviseEndsOnTERM(t *testing.T) {
	b := newBench(t)
	supPID, jobPgid := orphanLiveSupervise(t, b)
	if err := syscall.Kill(supPID, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM supervise %d: %v", supPID, err)
	}
	waitGoneOrFail(t, supPID, "supervise after SIGTERM")
	waitGoneOrFail(t, jobPgid, "the harness process group after SIGTERM to supervise")
}

// TestSuperviseEndsWhenItsPoolRootDisappears: a supervise whose pool directory is gone
// must exit. The CI leftovers held a t.TempDir that had already been removed.
func TestSuperviseEndsWhenItsPoolRootDisappears(t *testing.T) {
	b := newBench(t)
	supPID, _ := orphanLiveSupervise(t, b)
	if err := os.RemoveAll(b.pool); err != nil {
		t.Fatalf("removing the pool root: %v", err)
	}
	waitGoneOrFail(t, supPID, "supervise after its pool root disappeared")
}

// orphanStoppedSupervise is the CI leak: runner killed after spawn, supervisor SIGSTOP'd
// once it is an orphan, so TERM does not end it.
func orphanStoppedSupervise(t *testing.T, b *bench) int {
	t.Helper()
	b.inject()
	taskID := b.add("stopped leftover\nFAKE-FINDINGS 0\n")
	b.extraEnv = []string{
		"NOVA_SWARM_PAUSEPOINT=before-identify",
		"NOVA_SWARM_PAUSE_AFTER_ORPHAN=1",
		"NOVA_SWARM_KILLPOINT=after-spawn",
	}
	b.run()
	b.extraEnv = nil
	return readSupervisorPID(t, b.jobDir(taskID))
}

// orphanLiveSupervise starts a supervise that has identified and released a long-running
// harness, then kills the runner so the supervise is not this test's child.
func orphanLiveSupervise(t *testing.T, b *bench) (supPID, jobPgid int) {
	t.Helper()
	b.inject()
	taskID := b.add("live leftover\nFAKE-SLEEP 60\nFAKE-FINDINGS 0\n", "--deadline", "2m")
	b.extraEnv = []string{"NOVA_SWARM_KILLPOINT=after-spawn"}
	b.run()
	b.extraEnv = nil
	jobDir := b.jobDir(taskID)
	supPID = readSupervisorPID(t, jobDir)
	t.Cleanup(func() { reapSupervise(supPID, jobPgid) })
	p := mustOpenPool(t, b.pool)
	bound := testWaitBound()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		sf, err := p.ReadSlot(1)
		if err == nil && sf.State == swarm.SlotLaunched && sf.JobPgid > 0 {
			jobPgid = sf.JobPgid
			t.Cleanup(func() { reapSupervise(supPID, jobPgid) })
			return supPID, jobPgid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the supervisor never released a harness (slot 1 job_pgid still 0 after %s): %s", bound, jobDir)
	return 0, 0
}

func reapSupervise(pid, jobPgid int) {
	if pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGCONT)
		swarm.Reap(pid, "", swarm.TerminateGrace)
	}
	if jobPgid > 1 && jobPgid != pid {
		swarm.Reap(jobPgid, "", swarm.TerminateGrace)
	}
}

func waitGoneOrFail(t *testing.T, pid int, what string) {
	t.Helper()
	bound := testWaitBound()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if !processIsAlive(pid) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: pid %d is still alive after %s", what, pid, bound)
}
