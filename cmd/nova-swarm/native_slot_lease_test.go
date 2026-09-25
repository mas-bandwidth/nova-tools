package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ONE SLOT LEDGER (nova-tools#3877). A bench's capacity is bench:<b>:desired in Redis and
// the dealer is the one place a card is admitted or refused against it; `native` takes no
// file lease of its own. These tests pin the two halves of that on the native side: a
// launch with no slot store runs, and a launch still handed the retired --slots-store and
// --owner (a caller built before #3877) runs against a store whose share is full, the
// shape that refused seven dealt cards on batman, and writes no lease into it.

// TestNativeRunsWithNoSlotsStore: no --slots-store, no --owner, and a home with no
// nova-bench/slots directory at all. The run is not refused, the card finishes NATIVE OK,
// and no slot store is made on the way past.
func TestNativeRunsWithNoSlotsStore(t *testing.T) {
	bin := nativeHarness(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "a card\n")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("a native launch with no slot store runs, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "SLOTS REFUSED") || strings.Contains(stderr.String(), "no_slots_store") {
		t.Fatalf("native refused on a slot ledger it no longer keeps:\n%s", stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "NATIVE OK ") {
		t.Fatalf("the card finishes NATIVE OK:\n%s%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "nova-bench", "slots")); !os.IsNotExist(err) {
		t.Fatalf("native made a slot store under the home: %v", err)
	}
}

// TestNativeIgnoresTheRetiredSlotsStore: the batman shape, a store whose one owner holds
// its whole share. A caller that still passes --slots-store and --owner is not refused on
// an unknown flag and is not refused on the full share: the card runs, and the store holds
// exactly the leases it held before.
func TestNativeIgnoresTheRetiredSlotsStore(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "RESULT: schema-card sha=aaaaaaaaaaaa\nKIND: schema\na schema card\n")
	store := slotShares(t, "capacity\t1\nreserve\t0\nswarm-batman\t1\n")
	// The holder is ALIVE (this test's own pid), so the seat is held and never reaped.
	if err := swarm.MakeSlotLease(store, "held-1", "swarm-batman", os.Getpid(), "held", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	before := slotLeaseCount(t, store)

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
		"--label", "schema-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall",
		"--slots-store", store, "--owner", "swarm-batman"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("a dealt card is not refused by the retired file ledger, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "SLOTS REFUSED") {
		t.Fatalf("native refused on the file ledger:\n%s", stderr.String())
	}
	if after := slotLeaseCount(t, store); after != before {
		t.Fatalf("native wrote to the retired store: %d leases before, %d after", before, after)
	}
}
