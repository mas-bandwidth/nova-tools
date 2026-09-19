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
	rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
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
		rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
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
		rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
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

// TestNativeNoSlotsStoreTakesNothing: without --slots-store the feature is off — no lease
// is taken and the run behaves exactly as it does today.
func TestNativeNoSlotsStoreTakesNothing(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "a card\n")
	store := slotShares(t, "capacity\t2\nreserve\t0\nfake-1\t2\n")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("no --slots-store runs as today, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("the run behaves as today, got:\n%s", stdout.String())
	}
	if left := slotLeaseCount(t, store); left != 0 {
		t.Errorf("no --slots-store takes no lease, %d left", left)
	}
}
