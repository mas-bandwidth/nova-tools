package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// STELLA'S HOLD ON PR #1562, and it is a real ownership defect: `cmdNative`'s deferred
// cleanup released by OWNER AND LABEL, not by the lease this invocation took.
// `ReleaseSlotLeases(store, owner, label, false)` removes EVERY lease matching the owner
// and the label — so two `native` invocations that share an owner and a label do not hold a
// seat each, they hold a seat each that the other is free to give away.
//
// An owner and a label are not an identity. A bench has ONE owner by design, and a label is
// the card's name, which two slots, two benches and two retries reuse on purpose. This is
// not an exotic race; it is the ordinary case.
//
// Stella's own reproduction with the built CLI is the simplest and is the first case below:
// a MISSING HARNESS makes `native` exit 2 without ever starting a job, and the cleanup still
// deletes a pre-existing, live, unrelated lease.
//
// NOTHING HERE IS TIMED. The overlap between two live runs is held open by a BARRIER the
// fake harness blocks on — `FAKE-AWAIT-NOTE`, which waits for `<job>/note` to exist — and
// released by the test when it has finished looking. The repository's own note beside that
// directive is the rule being followed: "A WORKER THAT IS WAITED ON WAITS FOR THE THING
// ITSELF, NEVER FOR A CLOCK (#122)." Every wait below waits for a FILE the run creates, so
// a slow bench makes this test slow and never makes it wrong.
const identityOwner = "fake-1"

// barrierCard blocks the fake harness until the test writes `<job>/note`. The 60 is the
// harness's own outer bound, not this test's schedule: nothing here waits for it to expire.
const barrierCard = "FAKE-AWAIT-NOTE 60\n"

// plantLease puts a live lease in the store that this invocation does NOT own: the pid is
// this test process, so it is alive and cannot be reaped, and the until is an hour out.
func plantLease(t *testing.T, store, id, label string) {
	t.Helper()
	if err := swarm.MakeSlotLease(store, id, identityOwner, os.Getpid(), label, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func leaseExists(store, id string) bool {
	_, err := os.Stat(filepath.Join(store, "slots", id, "lease"))
	return err == nil
}

// assertBystandersSurvive is the whole contract in one helper: whatever this run did, the
// two leases it did not take are still there. `same` shares the run's label and is the one
// the defect deleted; `other` carries a different label and survived even with the defect,
// which is what tells a reader the label matching WAS the mechanism.
func assertBystandersSurvive(t *testing.T, store, what string) {
	t.Helper()
	if !leaseExists(store, "bystander-same-label") {
		t.Errorf("%s: the bystander lease sharing this run's owner AND label was deleted; a run releases the seat it took, never a seat that matches its name", what)
	}
	if !leaseExists(store, "bystander-other-label") {
		t.Errorf("%s: the bystander lease with a different label was deleted; this one survived even the defect, so its loss means something worse", what)
	}
}

func identityStore(t *testing.T) string {
	t.Helper()
	store := slotShares(t, "capacity\t8\nreserve\t0\nfake-1\t8\n")
	plantLease(t, store, "bystander-same-label", "shared-label")
	plantLease(t, store, "bystander-other-label", "a-different-label")
	return store
}

// jobFile is a path inside the job directory a run makes for `shared-label`.
func jobFile(slot, name string) string {
	return filepath.Join(slot, "jobs", "shared-label", name)
}

// waitForFile is recovery_test.go's, reused deliberately: it waits on an OBSERVABLE the
// run creates rather than on a clock, which is the same rule this file follows throughout.
// A timeout in it here means the bench is slow, not that the lease contract is wrong.

// waitForSeats waits for the store to hold n leases. Same rule: a timeout is the bench.
func waitForSeats(t *testing.T, store string, n int) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if slotLeaseCountNow(store) == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited 60s for %d seats and saw %d; this is the bench being slow, not the lease contract", n, slotLeaseCountNow(store))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// releaseBarrier lets a blocked fake harness finish, by writing the note it waits on.
func releaseBarrier(t *testing.T, slot string) {
	t.Helper()
	if err := os.WriteFile(jobFile(slot, "note"), []byte("go on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nativeArgs is one native launch against a store, as every case here runs it.
func nativeArgs(harness, card, slot, root, store, deadline string) []string {
	return []string{"native", "--harness", harness, "--model", "fake/fake-model",
		"--label", "shared-label", "--card", card, "--slot", slot, "--root", root,
		"--deadline", deadline, "--no-wall",
		"--slots-store", store, "--owner", identityOwner}
}

// The three exit paths that need no coordination, each with two bystander leases planted
// first. The deadline and the TERM are NOT here: they have their own tests, because each
// has an outcome to prove by name and a table that accepted any return code would pass on a
// refusal that never reached the thing it is named for.
func TestUnrelatedLeasesSurviveEveryNativeExitPath(t *testing.T) {
	realHarness := nativeHarness(t)

	for _, tc := range []struct {
		name    string
		harness string
		card    string
		wantRC  int
		why     string
	}{
		{
			// STELLA'S REPRODUCTION, and the simplest red in this file.
			name: "a_missing_harness_refuses_before_any_job", harness: "/nonexistent/harness/binary",
			card: "a card\n", wantRC: 2,
			why: "a missing harness exits 2 with no job started",
		},
		{
			name: "normal_completion", harness: realHarness, card: "a card\n", wantRC: 0,
			why: "a run that finished",
		},
		{
			name: "the_run_fails", harness: realHarness, card: "FAKE-RC 3\n", wantRC: 3,
			why: "a run whose card failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := identityStore(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			write(t, cardPath, tc.card)

			var stdout, stderr bytes.Buffer
			rc := run(nativeArgs(tc.harness, cardPath, slot, root, store, "60s"),
				strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != tc.wantRC {
				t.Fatalf("%s: exit %d, want %d:\n%s%s", tc.why, rc, tc.wantRC, stdout.String(), stderr.String())
			}

			assertBystandersSurvive(t, store, tc.why)
			if got := slotLeaseCount(t, store); got != 2 {
				t.Errorf("%s: the store holds %d leases and should hold exactly the 2 bystanders; the run kept or took a seat it should not have", tc.why, got)
			}
		})
	}
}

// THE DEADLINE, PROVED RATHER THAN ASSUMED. This case used to sit in the table above with a
// return code check of `func(int) bool { return true }` — it accepted anything, so a run
// that refused in preflight and never reached a deadline at all would have passed it. Stella
// held the PR on exactly that.
//
// So it asserts, in order: the real harness STARTED (it wrote its argv into the job), the
// run ended AT THE DEADLINE and not some other way (the `NATIVE OK` line, `rc=-1`, and NO
// `reason=terminated`, which is what separates this path from a TERM), the process's own
// exit code, that the run gave back ITS OWN seat, and that the bystanders are untouched.
func TestUnrelatedLeasesSurviveTheDeadline(t *testing.T) {
	store := identityStore(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	// The barrier is never released: the harness is blocked in it when the deadline fires,
	// which is the only way to be sure the deadline is what ended this run.
	write(t, cardPath, barrierCard)

	var stdout, stderr bytes.Buffer
	rc := run(nativeArgs(nativeHarness(t), cardPath, slot, root, store, "3s"),
		strings.NewReader(""), &stdout, &stderr, time.Now())

	if _, err := os.Stat(jobFile(slot, "argv")); err != nil {
		t.Fatalf("the harness never started, so this test never reached a deadline: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if rc != 1 {
		t.Errorf("a run cut at its deadline exits 1, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "NATIVE OK") {
		t.Fatalf("a run cut at its deadline still prints the NATIVE OK line:\n%s", out)
	}
	if !strings.Contains(out, " rc=-1 ") {
		t.Errorf("a run cut at its deadline reports rc=-1 for the child it killed:\n%s", out)
	}
	if strings.Contains(out, "reason=terminated") {
		t.Errorf("reason=terminated is the TERM path's word; a DEADLINE must not claim it:\n%s", out)
	}

	assertBystandersSurvive(t, store, "a run cut at its deadline")
	if got := slotLeaseCount(t, store); got != 2 {
		t.Errorf("a run cut at its deadline: the store holds %d leases and should hold exactly the 2 bystanders; the deadline path must give back its own seat and only its own", got)
	}
}

// The fifth exit path needs a real process to signal, so it is its own test: a TERM from
// outside, the path a manager takes when a run overruns. Its end reason is proved BY NAME —
// `reason=terminated`, which the deadline path above must not print and this one must.
func TestUnrelatedLeasesSurviveATermFromOutside(t *testing.T) {
	windowsIsNotABench(t)
	tool, _ := builtBinaries(t)
	store := identityStore(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, barrierCard)

	cmd := exec.Command(tool, nativeArgs(nativeHarness(t), cardPath, slot, root, store, "120s")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting native: %v", err)
	}
	// The harness has STARTED and is blocked in the barrier: both are files it wrote.
	waitForFile(t, jobFile(slot, "argv"), "the harness never started (a wait here is the bench, not the lease contract)")
	waitForSeats(t, store, 3) // two bystanders and this run's own seat

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Errorf("a TERMed run exits non-zero, got 0:\n%s", stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "reason=terminated") {
		t.Errorf("a TERM from outside ends the run with reason=terminated, and it is the word that separates this path from the deadline:\n%s", out)
	}
	if !strings.Contains(out, " rc=-1 ") {
		t.Errorf("a TERMed run reports rc=-1 for the child it reaped:\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(slot, "usage.tsv")); serr != nil {
		t.Errorf("usage.tsv absent after a TERM from outside: %v\n%s", serr, stderr.String())
	}

	assertBystandersSurvive(t, store, "a run TERMed from outside")
	if got := slotLeaseCount(t, store); got != 2 {
		t.Errorf("a run TERMed from outside: the store holds %d leases and should hold exactly the 2 bystanders", got)
	}
}

// Two live invocations sharing an owner and a label, in both directions, because they are
// different bugs wearing one cause: the second FAILS and its cleanup takes the first's live
// seat, or the first FINISHES and its cleanup takes the second's.
//
// THE OVERLAP IS HELD BY A BARRIER, NOT A SLEEP. Each run blocks in the fake harness until
// this test writes its note, so "both are running at once" is something the test ARRANGES
// and then ends, rather than something it hopes is still true.
func TestTwoNativeRunsSharingAnOwnerAndLabelKeepTheirOwnSeats(t *testing.T) {
	bin := nativeHarness(t)

	// start launches one barrier-blocked run and returns its exit channel and its slot.
	start := func(t *testing.T, store, card string) (<-chan int, string) {
		t.Helper()
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		write(t, cardPath, card)
		out := make(chan int, 1)
		go func() {
			var stdout, stderr bytes.Buffer
			out <- run(nativeArgs(bin, cardPath, slot, root, store, "120s"),
				strings.NewReader(""), &stdout, &stderr, time.Now())
		}()
		return out, slot
	}

	// stillRunning refuses to let a subtest pass by accident: if the run that is supposed
	// to be holding a seat has already finished, nothing was measured.
	stillRunning := func(t *testing.T, ch <-chan int, who string) {
		t.Helper()
		select {
		case code := <-ch:
			t.Fatalf("the %s run finished (exit %d) before the other released; the window closed and nothing was measured", who, code)
		default:
		}
	}

	t.Run("the_failing_second_leaves_the_first_running", func(t *testing.T) {
		store := slotShares(t, "capacity\t4\nreserve\t0\nfake-1\t4\n")
		first, firstSlot := start(t, store, barrierCard)
		waitForFile(t, jobFile(firstSlot, "argv"), "the first run's harness never started (a wait here is the bench, not the lease contract)")
		waitForSeats(t, store, 1)

		// The second shares the owner and the label and FAILS after acquiring its seat.
		second, _ := start(t, store, "FAKE-RC 3\n")
		if rc := <-second; rc != 3 {
			t.Fatalf("the second run is the failing one; it exited %d, want 3", rc)
		}

		stillRunning(t, first, "first")
		if got := slotLeaseCountNow(store); got != 1 {
			t.Fatalf("after the failing second run released, the store holds %d leases, want 1: the first run is STILL BLOCKED IN ITS BARRIER and its seat must still be there", got)
		}

		releaseBarrier(t, firstSlot)
		if code := <-first; code != 0 {
			t.Errorf("the first run exits 0 once released, got %d", code)
		}
		if left := slotLeaseCount(t, store); left != 0 {
			t.Errorf("both runs are done and %d leases are left", left)
		}
	})

	t.Run("the_first_to_finish_leaves_the_second_running", func(t *testing.T) {
		store := slotShares(t, "capacity\t4\nreserve\t0\nfake-1\t4\n")
		long, longSlot := start(t, store, barrierCard)
		waitForFile(t, jobFile(longSlot, "argv"), "the long run's harness never started (a wait here is the bench, not the lease contract)")
		waitForSeats(t, store, 1)
		short, shortSlot := start(t, store, barrierCard)
		waitForFile(t, jobFile(shortSlot, "argv"), "the short run's harness never started (a wait here is the bench, not the lease contract)")
		waitForSeats(t, store, 2)

		// Both are blocked and both hold a seat. Let ONE of them finish.
		releaseBarrier(t, shortSlot)
		if code := <-short; code != 0 {
			t.Fatalf("the short run exits 0 once released, got %d", code)
		}

		stillRunning(t, long, "long")
		if got := slotLeaseCountNow(store); got != 1 {
			t.Fatalf("after the short run released, the store holds %d leases, want 1: the long run is STILL BLOCKED IN ITS BARRIER and its seat must still be there", got)
		}

		releaseBarrier(t, longSlot)
		if code := <-long; code != 0 {
			t.Errorf("the long run exits 0 once released, got %d", code)
		}
		if left := slotLeaseCount(t, store); left != 0 {
			t.Errorf("both runs are done and %d leases are left", left)
		}
	})
}
