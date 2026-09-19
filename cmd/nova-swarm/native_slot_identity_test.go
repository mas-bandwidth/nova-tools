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
// and the label, so two `native` invocations that share an owner and a label do not hold a
// seat each — they hold a seat each that the other is free to give away.
//
// An owner and a label are not an identity. A bench has ONE owner by design, and a label is
// the card's name, which two slots, two benches and two retries reuse on purpose. This is
// not an exotic race; it is the ordinary case.
//
// Stella's own reproduction, with the built CLI, is the simplest one and is the first
// subtest below: a MISSING HARNESS makes `native` exit 2 without ever starting a job — and
// the cleanup still deletes a pre-existing, live, unrelated lease. No goroutines, no sleeps,
// no timing: the lease is planted, one command is run, the lease is gone.
//
// The damage is what the store is FOR. A seat released out from under a live run makes the
// bench answer "that slot is free" while a card is still burning it.
const identityOwner = "fake-1"

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

// The four in-process exit paths, each with two bystander leases planted first. Stella
// requires proof for every one of them, because the release is a `defer` and a `defer`
// fires on paths a reader does not think about.
func TestUnrelatedLeasesSurviveEveryNativeExitPath(t *testing.T) {
	realHarness := nativeHarness(t)

	for _, tc := range []struct {
		name     string
		harness  string
		card     string
		deadline string
		wantRC   func(int) bool
		why      string
	}{
		{
			// STELLA'S REPRODUCTION, and the simplest red in this file.
			name: "a_missing_harness_refuses_before_any_job", harness: "/nonexistent/harness/binary",
			card: "a card\n", deadline: "30s",
			wantRC: func(rc int) bool { return rc == 2 },
			why:    "a missing harness exits 2 with no job started",
		},
		{
			name: "normal_completion", harness: realHarness, card: "FAKE-SLEEP 1\n", deadline: "60s",
			wantRC: func(rc int) bool { return rc == 0 },
			why:    "a run that finished",
		},
		{
			name: "the_run_fails", harness: realHarness, card: "FAKE-RC 3\n", deadline: "60s",
			wantRC: func(rc int) bool { return rc != 0 },
			why:    "a run whose card failed",
		},
		{
			name: "the_deadline_cuts_it", harness: realHarness, card: "FAKE-SLEEP 60\n", deadline: "2s",
			wantRC: func(int) bool { return true },
			why:    "a run cut at its deadline",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := identityStore(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			write(t, cardPath, tc.card)

			var stdout, stderr bytes.Buffer
			rc := run([]string{"native", "--harness", tc.harness, "--model", "fake/fake-model",
				"--label", "shared-label", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", tc.deadline, "--no-wall",
				"--slots-store", store, "--owner", identityOwner},
				strings.NewReader(""), &stdout, &stderr, time.Now())
			if !tc.wantRC(rc) {
				t.Fatalf("%s: unexpected exit %d:\n%s%s", tc.why, rc, stdout.String(), stderr.String())
			}

			assertBystandersSurvive(t, store, tc.why)
			// And the run gave its OWN seat back: two bystanders, nothing else.
			if got := slotLeaseCount(t, store); got != 2 {
				t.Errorf("%s: the store holds %d leases and should hold exactly the 2 bystanders; the run kept or took a seat it should not have", tc.why, got)
			}
		})
	}
}

// The fifth exit path needs a real process to signal, so it is its own test: a TERM from
// outside, the path a manager takes when a run overruns.
func TestUnrelatedLeasesSurviveATermFromOutside(t *testing.T) {
	windowsIsNotABench(t)
	tool, _ := builtBinaries(t)
	bin := nativeHarness(t)
	store := identityStore(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "FAKE-SLEEP 60\n")

	cmd := exec.Command(tool, "native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "shared-label", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "60s", "--no-wall",
		"--slots-store", store, "--owner", identityOwner)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting native: %v", err)
	}
	argv := filepath.Join(slot, "jobs", "shared-label", "argv")
	waitFor := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(argv); err == nil {
			break
		}
		if time.Now().After(waitFor) {
			_ = cmd.Process.Kill()
			t.Fatalf("the harness never started (no argv); this is the bench being slow, not the lease contract:\n%s", stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	_ = cmd.Wait()

	assertBystandersSurvive(t, store, "a run TERMed from outside")
	if got := slotLeaseCount(t, store); got != 2 {
		t.Errorf("a run TERMed from outside: the store holds %d leases and should hold exactly the 2 bystanders", got)
	}
}

// Two live invocations sharing an owner and a label, in both directions, because they are
// different bugs wearing one cause: the second FAILS and its cleanup takes the first's live
// seat, or the first FINISHES and its cleanup takes the second's.
func TestTwoNativeRunsSharingAnOwnerAndLabelKeepTheirOwnSeats(t *testing.T) {
	bin := nativeHarness(t)

	start := func(t *testing.T, store, card string) <-chan int {
		t.Helper()
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		write(t, cardPath, card)
		out := make(chan int, 1)
		go func() {
			var stdout, stderr bytes.Buffer
			out <- run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
				"--label", "shared-label", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "90s", "--no-wall",
				"--slots-store", store, "--owner", identityOwner},
				strings.NewReader(""), &stdout, &stderr, time.Now())
		}()
		return out
	}

	// A timeout here is the BENCH being slow, not the contract being wrong, and it says so.
	waitForSeats := func(t *testing.T, store string, n int) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			if slotLeaseCountNow(store) == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("waited 30s for %d seats and saw %d; this is the bench being slow, not the lease contract", n, slotLeaseCountNow(store))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	t.Run("the_failing_second_leaves_the_first_running", func(t *testing.T) {
		store := slotShares(t, "capacity\t4\nreserve\t0\nfake-1\t4\n")
		first := start(t, store, "FAKE-SLEEP 20\n")
		waitForSeats(t, store, 1)

		if rc := <-start(t, store, "FAKE-RC 3\n"); rc == 0 {
			t.Fatalf("the second run is the failing one; it exited 0")
		}
		select {
		case code := <-first:
			t.Fatalf("the first run finished (exit %d) before the second released; the window closed and nothing was measured", code)
		default:
		}
		if got := slotLeaseCountNow(store); got != 1 {
			t.Fatalf("after the failing second run released, the store holds %d leases, want 1: the first run is STILL RUNNING and its seat must still be there", got)
		}
		if code := <-first; code != 0 {
			t.Errorf("the first run exits 0, got %d", code)
		}
		if left := slotLeaseCount(t, store); left != 0 {
			t.Errorf("both runs are done and %d leases are left", left)
		}
	})

	t.Run("the_first_to_finish_leaves_the_second_running", func(t *testing.T) {
		store := slotShares(t, "capacity\t4\nreserve\t0\nfake-1\t4\n")
		long := start(t, store, "FAKE-SLEEP 20\n")
		waitForSeats(t, store, 1)
		short := start(t, store, "FAKE-SLEEP 1\n")
		waitForSeats(t, store, 2)

		if code := <-short; code != 0 {
			t.Fatalf("the short run exits 0, got %d", code)
		}
		select {
		case code := <-long:
			t.Fatalf("the long run finished (exit %d) before the short one released; the window closed and nothing was measured", code)
		default:
		}
		if got := slotLeaseCountNow(store); got != 1 {
			t.Fatalf("after the short run released, the store holds %d leases, want 1: the long run is STILL RUNNING and its seat must still be there", got)
		}
		if code := <-long; code != 0 {
			t.Errorf("the long run exits 0, got %d", code)
		}
		if left := slotLeaseCount(t, store); left != 0 {
			t.Errorf("both runs are done and %d leases are left", left)
		}
	})
}
