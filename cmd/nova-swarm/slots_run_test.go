package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// RED TESTS FOR #880 ITEM 18: `nova-swarm run` HOLDS A BENCH SLOT LEASE PER TASK.
//
// The slots verbs already exist (docs/SPEC-SWARM.md, "Bench slot leases"): a store
// with shares.tsv, take/release/list, expiry fenced by live pids. What is missing is
// the launcher's side of the contract -- `run --slots-store <dir> --owner <name>`
// takes one lease before each task starts and releases it when the task ends. A
// refused take is a wait, never a launch past the share. These tests drive the real
// binary against the fake harness on a temp store; nothing reaches the network.

// slotShares writes a shares.tsv for these tests and returns the store directory.
func slotShares(t *testing.T, body string) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "slots-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(store, "shares.tsv"), body)
	return store
}

// slotLeaseCount counts the lease directories the store holds right now.
func slotLeaseCount(t *testing.T, store string) int {
	t.Helper()
	return slotLeaseCountNow(store)
}

func slotLeaseCountNow(store string) int {
	entries, err := os.ReadDir(filepath.Join(store, "slots"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// TestRunTakesOneSlotLeaseAtATimeAndReleasesIt is the share-1 contract: two tasks
// under one owner never hold more than one lease, so they run one after the other,
// and every lease is released again when its task ends.
func TestRunTakesOneSlotLeaseAtATimeAndReleasesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	store := slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
	b.add("the first task waits its turn\nFAKE-FINDINGS 1\nFAKE-SLEEP 1\n")
	b.add("the second task waits its turn\nFAKE-FINDINGS 1\nFAKE-SLEEP 1\n")

	// While the run holds its leases, a reader watches the store: share 1 must never
	// show two of them at once.
	stop := make(chan struct{})
	watched := make(chan struct{})
	most := 0
	var mu sync.Mutex
	go func() {
		defer close(watched)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n := slotLeaseCountNow(store)
			mu.Lock()
			if n > most {
				most = n
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	begin := time.Now()
	exit, stdout, stderr := b.run("--workers", "2", "--slots-store", store, "--owner", "fake-1")
	elapsed := time.Since(begin)
	close(stop)
	<-watched

	if exit != 0 {
		t.Fatalf("run exited %d, want 0:\n%s%s", exit, stdout, stderr)
	}
	if got := strings.Count(stdout, "RUN DONE id="); got != 2 {
		t.Fatalf("both tasks finish, got %d RUN DONE:\n%s%s", got, stdout, stderr)
	}
	mu.Lock()
	peak := most
	mu.Unlock()
	if peak > 1 {
		t.Errorf("share 1 holds one lease at a time, saw %d:\n%s", peak, stdout)
	}
	if elapsed < 1800*time.Millisecond {
		t.Errorf("two tasks that each sleep a second run one after the other under share 1, took %s:\n%s", elapsed, stdout)
	}
	if left := slotLeaseCount(t, store); left != 0 {
		t.Errorf("every lease is released when its task ends, %d left in the store", left)
	}
}

// TestRunReapsADeadPidLeaseOnTheNextTake: a lease a dead dispatcher left behind is
// reaped by the next take, so a bench restart is not blocked by a corpse.
func TestRunReapsADeadPidLeaseOnTheNextTake(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	const deadPid = 2147483647
	if swarm.Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	b := newBench(t)
	store := slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
	// A lease a previous dispatcher held, past its until and named by a pid that is gone.
	if err := swarm.MakeSlotLease(store, "dead-1", "ghost", deadPid, "an-old-task", time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	b.add("a task a dead dispatcher's lease must not block\nFAKE-FINDINGS 1\n")

	exit, stdout, stderr := b.run("--slots-store", store, "--owner", "fake-1")
	if exit != 0 {
		t.Fatalf("run exited %d, want 0 (the dead lease is reaped):\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE")
	if left := slotLeaseCount(t, store); left != 0 {
		t.Errorf("the dead lease and the task's lease are both gone, %d left", left)
	}
}

// TestRunWaitsWhenTheSlotLeaseIsRefusedAndNamesTheHolder: when another owner holds
// the only slot, the run prints one WAIT line naming the holder and never launches
// past the share.
func TestRunWaitsWhenTheSlotLeaseIsRefusedAndNamesTheHolder(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	t.Parallel()
	b := newBench(t)
	store := slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
	// The rival is ALIVE, so this lease is held and not reaped.
	if err := swarm.MakeSlotLease(store, "rival-1", "rival", os.Getpid(), "the-rival-task", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	id := b.add("a task that must wait for the rival's slot\nFAKE-FINDINGS 1\n", "--deadline", "3s")

	exit, stdout, stderr := b.run("--slots-store", store, "--owner", "fake-1")
	mustContain(t, "the WAIT line", stdout, "RUN WAIT slots owner=fake-1 holders=rival:1")
	if strings.Contains(stdout, "RUN START id="+id) || strings.Contains(stdout, "RUN DONE id="+id) {
		t.Errorf("a refused take never launches past the share:\n%s%s", stdout, stderr)
	}
	if exit == 0 {
		t.Errorf("a task that never got a slot in time is not a green run, got exit 0:\n%s%s", stdout, stderr)
	}
}
