package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE NATIVE BENCH SLOT LEASE (nova-tools#1546). `native --slots-store <dir> --owner <name>`
// takes ONE bench slot lease before the run starts and releases it on every exit path; a
// store that grants nothing is a refusal, not a run. These tests drive the command against
// a temp store, the same store the `run` side of the contract is tested with.

// TestNativeRefusesWhenSlotShareIsFullyHeld: a store whose only seat is already held by
// another owner is a SLOTS REFUSED line, an exit 2, and no job directory, no harness.
func TestNativeRefusesWhenSlotShareIsFullyHeld(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "a card\n")
	store := slotShares(t, "capacity\t1\nreserve\t0\nrival\t1\n")
	// The rival is ALIVE, so this lease is held and not reaped.
	if err := swarm.MakeSlotLease(store, "rival-1", "rival", os.Getpid(), "the-rival-task", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "10s", "--no-wall",
		"--slots-store", store, "--owner", "fake-1"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc == 0 {
		t.Fatalf("a fully-held slot is refused, got exit 0:\n%s%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "SLOTS REFUSED owner=fake-1 want=1 held=0 share=0 free=0 holders=rival:1") {
		t.Fatalf("the refusal is one SLOTS REFUSED line naming owner/want/held/share/free:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs", "lbl")); !os.IsNotExist(err) {
		t.Fatalf("no job directory is made when the slot is refused: %v", err)
	}
}

// TestNativeTakesOneSlotLeaseAndReleasesIt: with a free seat native takes exactly one
// lease, and after the run returns the store holds no lease for that owner — including
// when the run itself fails.
func TestNativeTakesOneSlotLeaseAndReleasesIt(t *testing.T) {
	bin := nativeHarness(t)

	t.Run("success", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		write(t, cardPath, "FAKE-SLEEP 1\n")
		store := slotShares(t, "capacity\t2\nreserve\t0\nfake-1\t2\n")

		// While the run holds its lease, a reader watches the store: exactly one lease
		// must appear, never zero and never two.
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

		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--no-wall",
			"--slots-store", store, "--owner", "fake-1"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		close(stop)
		<-watched

		if rc != 0 {
			t.Fatalf("a free seat runs, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
		}
		mu.Lock()
		peak := most
		mu.Unlock()
		if peak != 1 {
			t.Errorf("native takes exactly one lease, saw peak %d", peak)
		}
		if left := slotLeaseCount(t, store); left != 0 {
			t.Errorf("the lease is released after the run, %d left", left)
		}
	})

	t.Run("run_fails", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		write(t, cardPath, "FAKE-RC 3\n")
		store := slotShares(t, "capacity\t2\nreserve\t0\nfake-1\t2\n")

		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--no-wall",
			"--slots-store", store, "--owner", "fake-1"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc == 0 {
			t.Fatalf("the failing run exits non-zero, got 0:\n%s%s", stdout.String(), stderr.String())
		}
		if left := slotLeaseCount(t, store); left != 0 {
			t.Errorf("the lease is released even when the run fails, %d left", left)
		}
	})
}

// TestNativeWithoutASlotsStoreRefuses: a launch without a lease is refused (SPEC-SWARM,
// "Bench slot leases"; nova-tools#1546). This test used to assert the OPPOSITE -- it was
// TestNativeNoSlotsStoreTakesNothing, and it said "without --slots-store the feature is
// off ... the run behaves exactly as it does today". That sentence is the hole: a native
// launch that takes no lease is one the bench cannot see, cannot count and cannot refuse,
// and a test asserting it RUNS pinned the hole open. Johnny held PR #1562 on exactly this.
//
// The refusal is checked WHOLE and checked to be ONE line. A remedy is a sentence someone
// reads at a prompt with a failed launch in front of them, so its wording is the promise,
// and a second line would mean the reader has to work out which of two things to do.
func TestNativeWithoutASlotsStoreRefuses(t *testing.T) {
	bin := nativeHarness(t)
	const want = swarm.NoSlotsStoreRefusal

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"neither", nil},
		{"store_without_owner", []string{"--slots-store", "STORE"}},
		{"owner_without_store", []string{"--owner", "fake-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			write(t, cardPath, "a card\n")
			store := slotShares(t, "capacity\t2\nreserve\t0\nfake-1\t2\n")

			argv := []string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
				"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s", "--no-wall"}
			for _, a := range tc.args {
				if a == "STORE" {
					a = store
				}
				argv = append(argv, a)
			}

			var stdout, stderr bytes.Buffer
			rc := run(argv, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != 2 {
				t.Fatalf("a launch with no bench slot lease is refused with exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
			}
			if got := strings.TrimSuffix(stderr.String(), "\n"); got != want {
				t.Errorf("the refusal is exactly\n  %s\nand it printed\n  %s", want, got)
			}
			if n := len(strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")); n != 1 {
				t.Errorf("the refusal is ONE line, got %d:\n%s", n, stderr.String())
			}
			if stdout.String() != "" {
				t.Errorf("a refusal writes nothing to stdout, got: %q", stdout.String())
			}
			if _, err := os.Stat(filepath.Join(slot, "jobs", "lbl")); !os.IsNotExist(err) {
				t.Errorf("no job directory is made when the launch is refused: %v", err)
			}
			if left := slotLeaseCount(t, store); left != 0 {
				t.Errorf("a refused launch holds no lease, %d left", left)
			}
		})
	}
}
