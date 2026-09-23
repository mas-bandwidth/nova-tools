package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ISSUE #1582. The dispatcher released bench slot leases by owner and label, the
// same shape Stella held #1562 on. TakeSlotLeases labels a run's lease with the
// task id, and ReleaseSlotLeases(store, owner, taskID, false) then removes EVERY
// lease matching that pair. Two dispatchers sharing an owner and a task id —
// two pools, a retry, an adoption of a holder that is still alive — each gave
// away the other's seat.
//
// The live-pid fence is not identity: both leases here name this process, so
// that fence lets the steal through. The by-hand verb `slots release --owner …
// --label …` is meant to stay as it is. It is the deferred cleanup that must
// not use it.

const dispatcherIdentityOwner = "bench"

func plantDispatcherBystanders(t *testing.T, store, taskID string) {
	t.Helper()
	until := time.Now().UTC().Add(time.Hour)
	pid := os.Getpid()
	if err := MakeSlotLease(store, "bystander-same-label", dispatcherIdentityOwner, pid, taskID, until); err != nil {
		t.Fatal(err)
	}
	if err := MakeSlotLease(store, "bystander-other-label", dispatcherIdentityOwner, pid, "a-different-task", until); err != nil {
		t.Fatal(err)
	}
}

func assertDispatcherBystandersSurvive(t *testing.T, store, what string) {
	t.Helper()
	held := heldIDs(t, store)
	if !held["bystander-same-label"] {
		t.Errorf("%s: the bystander lease sharing this run's owner AND task id was deleted; a dispatcher releases the seat it took, never a seat that matches its name", what)
	}
	if !held["bystander-other-label"] {
		t.Errorf("%s: the bystander lease with a different label was deleted; this one survived even the defect, so its loss means something worse", what)
	}
	if got := len(held); got != 2 {
		t.Errorf("%s: the store holds %d leases and should hold exactly the 2 bystanders; the run kept or took a seat it should not have: %v", what, got, held)
	}
}

// The three dispatcher paths that give the seat back all went through
// releaseSlotLease, which selected by owner and label. Launch-failed and
// launch-refused are the two that need no supervisor: each takes a lease, then
// the cleanup fires. The finish path uses the same helper.
func TestDispatcherReleasesSlotLeasesByIdentityNotOwnerAndLabel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sc      func(id string) Sidecar
		extra   func(*RunInput)
		wantOut string
		why     string
	}{
		{
			name:    "launch_failed_missing_supervisor",
			sc:      func(id string) Sidecar { return Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1} },
			extra:   func(in *RunInput) { in.Supervisor = filepath.Join(in.Pool.Dir, "no-such-supervisor") },
			wantOut: "RUN LAUNCH-FAILED",
			why:     "a missing supervisor exits launch-failed after the take",
		},
		{
			name: "launch_refused_over_max_input",
			sc: func(id string) Sidecar {
				return Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, MaxInput: 1}
			},
			wantOut: "RUN INPUT-LIMIT",
			why:     "a task over its max_input is refused after the take",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p, w := recoveryPool(t, dir)
			store := writeSlotStore(t, "capacity\t4\nreserve\t0\nbench\t4\n")
			id := "shared-task"
			plantDispatcherBystanders(t, store, id)
			if err := p.Add([]byte("a task two dispatchers can share an id for"), tc.sc(id)); err != nil {
				t.Fatal(err)
			}

			var out, errb bytes.Buffer
			in := RunInput{
				Pool: p, Worker: w, Workers: 1, Hours: 0.0001,
				Stdout: &out, Stderr: &errb, NoSandbox: true,
				Now:        func() time.Time { return time.Now().UTC() },
				SlotsStore: store, SlotOwner: dispatcherIdentityOwner, SlotPID: os.Getpid(),
			}
			if tc.extra != nil {
				tc.extra(&in)
			}
			_ = Run(in)
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("%s: the run never reached %s, so it never took a lease to release:\n%s%s",
					tc.why, tc.wantOut, out.String(), errb.String())
			}
			assertDispatcherBystandersSurvive(t, store, tc.why)
		})
	}
}
