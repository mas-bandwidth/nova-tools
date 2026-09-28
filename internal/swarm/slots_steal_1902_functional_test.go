//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// ISSUE #1902. A slot lease is an admission ticket and it is not a live fence:
// deleting it does not stop the process holding it. `slots release --owner --label`
// therefore freed a LIVE native's only seat, a second native took that seat, and two
// cards ran on a capacity-1 bench with both printing NATIVE OK. The same code path is
// what a card given --no-wall used to drop a bystander's lease from inside its own
// shell. `--owner` is an unauthenticated string and every owner on a shared bench is
// the same unix user, so "who called release" is not a fence either.
//
// The bound this test crosses is the accounting one: a seat whose holder is still
// running is still occupied, so it cannot be granted to anybody else. Releasing your
// OWN seat stays allowed -- that is how run and native give a seat back.
func TestReleaseKeepsASeatWhoseHolderIsStillRunning(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	writeShares1902(t, store, "capacity\t1\nreserve\t0\nalice\t1\n")

	// A real live process that is not this one: sleep, killed by the test.
	holder := exec.Command("sleep", "120")
	if err := holder.Start(); err != nil {
		t.Skipf("no sleep on this bench: %v", err)
	}
	defer func() { _ = holder.Process.Kill(); _, _ = holder.Process.Wait() }()

	if _, _, _, _, _, ok, err := TakeSlotLeases(store, "alice", 1, time.Hour, "victim", time.Now().UTC(), holder.Process.Pid); err != nil || !ok {
		t.Fatalf("the victim could not take its seat: ok=%v err=%v", ok, err)
	}

	released, held, live, err := ReleaseSlotLeasesForcing(store, "alice", "victim", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if released != 0 || live != 1 || held != 1 {
		t.Fatalf("release freed a live seat: released=%d held=%d live=%d, want 0/1/1", released, held, live)
	}
	// And the bench is still full: the thief cannot take what the victim is sitting in.
	if _, _, _, _, _, ok, err := TakeSlotLeases(store, "alice", 1, time.Hour, "thief", time.Now().UTC(), os.Getpid()); err != nil || ok {
		t.Fatalf("a second run took the victim's seat: ok=%v err=%v", ok, err)
	}

	// --force is the loud override a person has when they know what the store cannot.
	released, _, live, err = ReleaseSlotLeasesForcing(store, "alice", "victim", false, true)
	if err != nil || released != 1 || live != 0 {
		t.Fatalf("--force did not free the seat: released=%d live=%d err=%v", released, live, err)
	}
}
