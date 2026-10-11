//go:build functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
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
// has "its own data home" and "a slot is held by exactly one worker". A receipt
// had both runs print NATIVE OK naming the same opencode.db.
func TestASecondNativeInOneSlotUnderAnotherLabelIsRefused(t *testing.T) {
	t.Parallel()

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

	// A is running the moment its HARNESS is: the fake writes its argv first thing, and
	// the harness starts only after the run has taken both of its leases. That readiness
	// signal exists on BOTH sides of this repair, so a run without the slot lease reaches
	// the assertions below rather than waiting for a file the hole means is never written.
	//
	// IT USED TO BE A's JOB LEASE (issue #2993), and the job lease is taken BEFORE the
	// slot lease: B could be admitted into that gap, take the slot itself, and A was the
	// one refused -- `a second run in a live slot exits 2, got 0` and `the live run holds
	// no slot lease`, both at once, in 0.07 s on hosted macOS at 2a43d771.
	awaitNativeFile(t, filepath.Join(slot, "jobs", "card-a", "argv"))
	slotLease := filepath.Join(slot, swarm.SlotLeaseName)

	// B: the same slot, a DIFFERENT label. Different job directory, same data home.
	var errB bytes.Buffer
	_, codeB := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-b",
		card:    []byte("a second card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errB)

	assert.Equal(t, 2, codeB, "a second run in a live slot exits 2, got %d:\n%s", codeB, errB.String())
	assert.Contains(t, errB.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errB.String())
	assert.Contains(t, errB.String(), "slot", "the refusal does not name the slot and its holder:\n%s", errB.String())
	assert.Contains(t, errB.String(), "pid=", "the refusal does not name the slot and its holder:\n%s", errB.String())
	// B wrote nothing of A's, and did not start its own harness against A's data home.
	_, err := os.Stat(filepath.Join(slot, "jobs", "card-b", "argv"))
	assert.Error(t, err, "the refused run's harness ran anyway against %s", filepath.Join(slot, "data"))
	held, err := os.ReadFile(slotLease)
	require.NoError(t, err, "the live run holds no slot lease at %s", slotLease)
	assert.Contains(t, string(held), "label=card-a\n", "the slot lease does not name the card holding it:\n%s", held)

	// Let A finish on its own terms; its release takes the slot lease with it.
	require.NoError(t, os.WriteFile(filepath.Join(slot, "jobs", "card-a", "note"), []byte("go on\n"), 0o644))
	got := <-first
	require.Equal(t, 0, got.code, "the first run did not finish cleanly: exit %d\n%s", got.code, got.err)
	_, err = os.Lstat(slotLease)
	assert.True(t, os.IsNotExist(err), "the finished run left its slot lease behind: %v", err)

	// And the slot is takeable again, by another label: the refusal is about a LIVE
	// holder, not about the slot having been used once.
	var errC bytes.Buffer
	_, codeC := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-c",
		card:    []byte("a third card\n"),
		slotDir: slot, root: root, deadline: time.Minute, noWall: true,
	}, &errC)
	require.Equal(t, 0, codeC, "a freed slot did not take a new run: exit %d\n%s", codeC, errC.String())
}

// TestASecondNativeInOneSlotControlWithoutTheSlotLeaseIsAdmitted is the control for the
// test above (issue #2993): the same two runs, with the live holder's slot lease taken
// away once it is running -- the refusal removed, from B's side. B must then be ADMITTED
// and run its harness against A's data home, which is exactly the assertions above
// failing. Nothing here waits on a clock; each step waits on the file that says it
// happened.
func TestASecondNativeInOneSlotControlWithoutTheSlotLeaseIsAdmitted(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)

	first := make(chan int, 1)
	go func() {
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "card-a",
			card:    []byte("FAKE-AWAIT-NOTE 120\n"),
			slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
		}, &errOut)
		first <- code
	}()
	awaitNativeFile(t, filepath.Join(slot, "jobs", "card-a", "argv"))
	require.NoError(t, os.Remove(filepath.Join(slot, swarm.SlotLeaseName)), "the running holder had no slot lease to take away")

	var errB bytes.Buffer
	_, codeB := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-b",
		card:    []byte("a second card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errB)
	require.NoError(t, os.WriteFile(filepath.Join(slot, "jobs", "card-a", "note"), []byte("go on\n"), 0o644))
	<-first

	require.NotEqual(t, 2, codeB, "with the holder's slot lease gone, B was refused anyway -- the test above cannot tell the refusal from its absence:\n%s", errB.String())
	require.NotContains(t, errB.String(), "NATIVE REFUSED", "with the holder's slot lease gone, B was refused anyway -- the test above cannot tell the refusal from its absence:\n%s", errB.String())
	_, err := os.Stat(filepath.Join(slot, "jobs", "card-b", "argv"))
	require.NoError(t, err, "with the holder's slot lease gone, B's harness never ran:\n%s", errB.String())
}

// awaitNativeFile waits for a file a run is expected to write. The bound is a safety net
// for a run that never gets there; the assertion is the file.
func awaitNativeFile(t *testing.T, path string) {
	t.Helper()
	for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return
		}
	}
	t.Fatalf("%s never appeared: the first run's harness never started", path)
}
