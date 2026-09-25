//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// testAttest is the secret the fixtures plant as the supervisor's own, so the exit.json they
// write carries an attestation whose hash matches the slot file they plant beside it.
const testAttest = "test-supervisor-secret"

func mustOpenPool(t *testing.T, dir string) *swarm.Pool {
	t.Helper()
	p, err := swarm.OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// runWatching now lives in swarm_test.go, because the audit-lessons test that kills an
// adopted job on its RUN ADOPT line is compiled on every platform and the recovery tests
// are not; the helper carries the windows environment swarmTry adds.

func assertTripwire(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	swarmDir := filepath.Join(root, "internal", "swarm")
	entries, err := os.ReadDir(swarmDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(swarmDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			for _, forbidden := range []string{"pgrep", `"ps"`, `"ps `, `exec.Command("ps"`} {
				if strings.Contains(trimmed, forbidden) {
					t.Errorf("%s contains forbidden %s (tripwire: no pgrep or ps)", e.Name(), forbidden)
				}
			}
		}
	}
}

// waitGone waits for a process THIS PROCESS DID NOT FORK to be gone, and it has a deadline:
// a supervisor whose runner was killed is init's child, so there is nothing to reap here and
// the only observable is the pid. The deadline is long because it is not a measurement --
// what is measured is what is on disk at the moment the process is gone.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	for waited := time.Duration(0); waited < 30*time.Second; waited += 5 * time.Millisecond {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("supervisor %d was still alive 30s after its SIGCONT", pid)
}

// waitGroupGone waits for a process GROUP to be gone, and it has a deadline. It is the
// second half of "the launch transaction is over": a supervisor's pid can be gone while a
// member of the job's group is still draining, and Pool.Decide reads that survivor as a
// quarantine rather than the reclaim the caller is about to assert. A pgid of zero is a
// group that was never recorded, which is nothing to wait for.
func waitGroupGone(t *testing.T, pgid int, started, what string) {
	t.Helper()
	if pgid <= 0 {
		return
	}
	bound := testWaitBound()
	for waited := time.Duration(0); waited < bound; waited += 5 * time.Millisecond {
		if !swarm.GroupAlive(pgid, started) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: process group %d was still alive after %s", what, pgid, bound)
}

// readSupervisorPID reads the pid the dispatcher wrote before its handshake. Every subtest
// that needs to wait on a supervisor it did not fork starts here.
func readSupervisorPID(t *testing.T, jobDir string) int {
	t.Helper()
	rawPid, err := os.ReadFile(filepath.Join(jobDir, "supervisor.pid"))
	if err != nil {
		t.Fatalf("could not read supervisor.pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPid)))
	if err != nil {
		t.Fatalf("bad supervisor pid: %v", err)
	}
	return pid
}

// waitStopped waits for a process to actually be stopped, and it has a deadline. `ps` is
// the one answer both platforms of this test give: T is "stopped", on darwin and on linux
// alike.
func waitStopped(t *testing.T, pid int) {
	t.Helper()
	last := ""
	for waited := time.Duration(0); waited < 30*time.Second; waited += 5 * time.Millisecond {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		last = strings.TrimSpace(string(out))
		if err == nil && strings.HasPrefix(last, "T") {
			return
		}
		if err != nil || last == "" {
			t.Fatalf("supervisor %d was gone before it ever stopped: a hangup, not a pause (ps: %q, %v)", pid, last, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("supervisor %d never reached its pause point; ps says %q", pid, last)
}

// testWaitBound is how long a poll waits for an observable before it gives up. A test reads
// it from the environment so a loaded runner can be given more time without editing the
// test; the default is generous because every wait that uses it returns the MOMENT the
// observable appears, and a slow box pays only when the fact never arrives.
func testWaitBound() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// waitIdentified waits for a supervisor to have IDENTIFIED itself, which the pool exposes
// as the task's slot reading `launched` -- the same state the adopting run decides from.
// It is NOT supervisor.pid: the dispatcher writes that file when it forks the supervisor,
// before the supervisor has run at all and while the launch still reads reserved, so a wait
// on it ended instantly and the adopting run met a reservation under load, quarantined it,
// and the test never saw RUN ADOPT. The wait is event-driven -- 50 ms polls on the code's own
// state, returned as soon as it lands -- and the bound is generous only so a launch that
// never identifies is reported rather than waited on forever.
func waitIdentified(t *testing.T, b *bench, taskID string) {
	t.Helper()
	p := mustOpenPool(t, b.pool)
	for waited := time.Duration(0); waited < testWaitBound(); waited += 50 * time.Millisecond {
		if sf, err := p.ReadSlot(1); err == nil && sf.State == swarm.SlotLaunched {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the supervisor never identified itself (slot 1 never read %q), so the adopting run would meet a reservation: %s",
		swarm.SlotLaunched, b.jobDir(taskID))
}

// adoptAndNote runs the dispatcher once and, the moment it names the adopted job, delivers
// the note that lets the worker finish. Both sides wait on an observable -- the dispatcher on
// the live supervisor, the worker on the note -- so an adopted job that must stay alive is
// not kept alive by a clock, and the test does not sleep on one either.
func adoptAndNote(t *testing.T, b *bench, taskID string) (int, string, string) {
	t.Helper()
	var noteErr error
	noted := false
	runArgs := withSandbox([]string{"run", "--pool", b.pool, "--workers", "1", "--hours", "0.25", "--worker", b.worker})
	exit, stdout, stderr := b.runWatching(runArgs, func(line string) {
		if noted || !strings.Contains(line, "RUN ADOPT id="+taskID) {
			return
		}
		noted = true
		nExit, _, nErr, err := b.swarmTry("note", "--pool", b.pool, "--task", taskID, "--text", "the test says finish now")
		switch {
		case err != nil:
			noteErr = err
		case nExit != 0:
			noteErr = fmt.Errorf("the note was refused (exit %d): %s", nExit, nErr)
		}
	})
	if !noted {
		t.Fatalf("the dispatcher never adopted %s (no RUN ADOPT), so this test proved nothing:\n%s", taskID, stdout)
	}
	if noteErr != nil {
		t.Fatalf("the note that lets the adopted job finish: %v", noteErr)
	}
	return exit, stdout, stderr
}

// waitForFile waits for a file this test's owns to appear, and gives up on its own. It is
// a wait on an OBSERVABLE -- the durable evidence the launch transaction owes -- and never
// a sleep racing a process: the second dispatcher below runs only once the file says the
// transaction finished, however long the kernel's hangup takes to resolve. The 30s bound is
// a safety net for a supervisor that never recovers; the assertion is on the file, not the
// elapsed time.
func waitForFile(t *testing.T, path, what string) {
	t.Helper()
	for waited := time.Duration(0); waited < 30*time.Second; waited += 5 * time.Millisecond {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: %s never appeared", what, path)
}

// waitForSlotLaunched waits for a supervisor to write its identity into its slot file. It is
// the observable that replaces the wall-clock sleep the after-spawn subtest used to race the
// supervisor's start-up; a dispatcher that adopts only runs a launch the slot file says
// reached `launched`.
func waitForSlotLaunched(t *testing.T, p *swarm.Pool, slot int) {
	t.Helper()
	for waited := time.Duration(0); waited < 30*time.Second; waited += 5 * time.Millisecond {
		if sf, err := p.ReadSlot(slot); err == nil && sf.State == swarm.SlotLaunched {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("slot %d never reached launched", slot)
}

// THE SUPERVISORS THESE TESTS FORK ARE STOPPED OR ORPHANED ON PURPOSE, and a subtest that
// dies before it resumes or reaps its own leaves that stop behind forever: a supervisor
// paused at before-identify that nobody ever SIGCONT'd is the six `supervise` processes
// that were alive on Space for 14 hours. Every subtest that reads a supervisor's pid hands
// it here; t.Cleanup reaps it whether the subtest passed or failed, and the final subtest
// proves none survived.
var (
	spawnedSupsMu sync.Mutex
	spawnedSups   = map[int]bool{}
)

// noteSupervisor records a forked supervisor so the subtest's cleanup reaps it and the
// test's end can prove none survived it.
func noteSupervisor(t *testing.T, pid int) {
	t.Helper()
	spawnedSupsMu.Lock()
	spawnedSups[pid] = true
	spawnedSupsMu.Unlock()
	t.Cleanup(func() {
		// A SIGSTOP'd process cannot die, only be resumed; SIGCONT first, then terminate,
		// wait, kill, and wait until the pid is gone so a leftover cannot outlive the test.
		_ = syscall.Kill(pid, syscall.SIGCONT)
		swarm.Reap(pid, "", swarm.TerminateGrace)
		for waited := time.Duration(0); waited < testWaitBound() && processIsAlive(pid); waited += 5 * time.Millisecond {
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// assertNoSurvivingSupervisor is the end-of-test proof: nothing these subtests forked is
// still alive after its own t.Cleanup ran. It asks the kernel about each pid the test noted
// -- never a scanned process table -- and waits only the bound a SIGKILL needs to land.
func assertNoSurvivingSupervisor(t *testing.T) {
	t.Helper()
	spawnedSupsMu.Lock()
	pids := make([]int, 0, len(spawnedSups))
	for pid := range spawnedSups {
		pids = append(pids, pid)
	}
	spawnedSupsMu.Unlock()
	for _, pid := range pids {
		for waited := time.Duration(0); waited < 30*time.Second && processIsAlive(pid); waited += 5 * time.Millisecond {
			time.Sleep(5 * time.Millisecond)
		}
		if processIsAlive(pid) {
			t.Errorf("a supervisor (pid %d) survived the subtest that forked it", pid)
		}
	}
}
