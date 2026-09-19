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

// ISSUE #1901. The bench store's lease is a COUNT: it says an owner holds n seats and
// it does not say WHICH slot directory a seat is. #1585's job lease refuses a second
// run in the same <slot>/jobs/<label>; it cannot refuse a second run in the same SLOT
// under a DIFFERENT label. And the data home is per slot, not per job
// (`dataHome := filepath.Join(cfg.slotDir, "data")`), so two labels in one slot is one
// HOME, one cache and one opencode.db -- the 2026-09-10 `database is locked` failure
// SPEC-SWARM's "the races, taken out" closed on purpose.
//
// The bound this test crosses is SPEC-SWARM's own sentence under **Slots**: a worker
// has "its own data home" and "a slot is held by exactly one worker". Johnny's receipt
// had both runs print NATIVE OK naming the same opencode.db.
func TestASecondNativeInOneSlotUnderAnotherLabelIsRefused(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	type outcome struct {
		code int
		err  string
	}
	first := make(chan outcome, 1)
	go func() {
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "card-a",
			card:    []byte("FAKE-AWAIT-NOTE 120\n"),
			slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
		}, &errOut)
		first <- outcome{code: code, err: errOut.String()}
	}()

	// A is running the moment its JOB lease is on disk. That is the readiness signal
	// that exists on BOTH sides of this repair, so a run without the slot lease reaches
	// the assertions below rather than waiting out the clock for a file the hole means
	// is never written.
	awaitLease(t, filepath.Join(slot, "jobs", "card-a", swarm.JobLeaseName))
	slotLease := filepath.Join(slot, swarm.SlotLeaseName)

	// B: the same slot, a DIFFERENT label. Different job directory, same data home.
	var errB bytes.Buffer
	_, codeB := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-b",
		card:    []byte("a second card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errB)

	if codeB != 2 {
		t.Errorf("a second run in a live slot exits 2, got %d:\n%s", codeB, errB.String())
	}
	if !strings.Contains(errB.String(), "NATIVE REFUSED") {
		t.Errorf("the refusal is one REFUSED line, got:\n%s", errB.String())
	}
	if !strings.Contains(errB.String(), "slot") || !strings.Contains(errB.String(), "pid=") {
		t.Errorf("the refusal does not name the slot and its holder:\n%s", errB.String())
	}
	// B wrote nothing of A's, and did not start its own harness against A's data home.
	if _, err := os.Stat(filepath.Join(slot, "jobs", "card-b", "argv")); err == nil {
		t.Errorf("the refused run's harness ran anyway against %s", filepath.Join(slot, "data"))
	}
	held, err := os.ReadFile(slotLease)
	if err != nil {
		t.Fatalf("the live run holds no slot lease at %s: %v", slotLease, err)
	}
	if !strings.Contains(string(held), "label=card-a\n") {
		t.Errorf("the slot lease does not name the card holding it:\n%s", held)
	}

	// Let A finish on its own terms; its release takes the slot lease with it.
	if err := os.WriteFile(filepath.Join(slot, "jobs", "card-a", "note"), []byte("go on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := <-first
	if got.code != 0 {
		t.Fatalf("the first run did not finish cleanly: exit %d\n%s", got.code, got.err)
	}
	if _, err := os.Lstat(slotLease); !os.IsNotExist(err) {
		t.Errorf("the finished run left its slot lease behind: %v", err)
	}

	// And the slot is takeable again, by another label: the refusal is about a LIVE
	// holder, not about the slot having been used once.
	var errC bytes.Buffer
	_, codeC := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-c",
		card:    []byte("a third card\n"),
		slotDir: slot, root: root, deadline: time.Minute, noWall: true,
	}, &errC)
	if codeC != 0 {
		t.Fatalf("a freed slot did not take a new run: exit %d\n%s", codeC, errC.String())
	}
}
