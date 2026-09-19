package jobs_test

// The kernel's side of docs/SPEC-WORKLANG.md Amendment 1, which the reader
// slice (#1350) named red and left unimplemented, and of docs/SPEC-JOBS.md
// section 9. Every subtest below carries the spec's OWN name for it, so a
// reader of either document can run the rule by name:
//
//	go test ./internal/jobs/ -run 'TestJobsAdmission/jobs-admission-is-atomic-no-partial-grant'
//
// Nothing here reads a clock, a file or a network. Admission is in-memory,
// single-writer and deterministic, so a rule that is green is green for a
// reason and not because a machine was quick.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

func TestJobsAdmission(t *testing.T) {
	// A5, A8 jobs-admission-is-atomic-no-partial-grant: admission takes the
	// whole vector or none of it. A request short on ONE dimension takes
	// nothing -- not the dimensions that were free -- so no unit ever holds a
	// partial grant it then waits inside, which is the deadlock A8 forbids.
	t.Run("jobs-admission-is-atomic-no-partial-grant", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 4, "memory-gb": 8})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "compile", Vector: jobs.Vector{"cpu": 3, "memory-gb": 2}}); err != nil {
			t.Fatalf("the first vector fits the authority whole, yet: %v", err)
		}
		// cpu is short by one; memory-gb is free. The whole request must fail.
		_, err := a.Grant(jobs.Request{ID: "link", Vector: jobs.Vector{"cpu": 2, "memory-gb": 4}})
		if err == nil {
			t.Fatal("granted a vector the authority cannot carry whole")
		}
		ref := mustRefusal(t, err)
		if ref.Dim != "cpu" {
			t.Errorf("refusal names %q, want the dimension that is short: cpu", ref.Dim)
		}
		if ref.Want != 2 || ref.Free != 1 {
			t.Errorf("refusal says want=%d free=%d, want 2 and 1", ref.Want, ref.Free)
		}
		// The dimension that WAS free must not have been taken on the way out.
		if _, held := a.Held("link"); held {
			t.Error("a refused request is holding a grant")
		}
		snap := a.Snapshot()
		for _, d := range snap.Dims {
			if d.Name == "memory-gb" && d.Used != 2 {
				t.Errorf("memory-gb used = %d after a refused request, want 2: the refusal took memory on its way out", d.Used)
			}
		}
		// And the proof that nothing was taken: the memory the refused request
		// would have held is still grantable to someone else.
		if _, err := a.Grant(jobs.Request{ID: "pack", Vector: jobs.Vector{"memory-gb": 6}}); err != nil {
			t.Fatalf("memory-gb was charged to a request that was refused: %v", err)
		}
		if got, want := a.Snapshot().Line(), "JOBS live=2 lanes=- cpu=3/4 memory-gb=8/8 writes=0"; got != want {
			t.Errorf("status line = %q, want %q", got, want)
		}
	})

	// A8 jobs-a-nested-grant-draws-from-its-parent: every other scheduler on a
	// machine holds a DELEGATED sub-budget, never an independent count of the
	// same cores. A child's grant comes out of its parent's remaining
	// reservation -- not out of the machine -- and goes back into it on
	// release. The day's evidence for the rule is load average 147 on the
	// Studio with four independent counters over 32 cores.
	t.Run("jobs-a-nested-grant-draws-from-its-parent", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 8})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "bench:hulk/alloc-7", Vector: jobs.Vector{"cpu": 4}}); err != nil {
			t.Fatalf("the parent reservation: %v", err)
		}
		if _, err := a.Grant(jobs.Request{ID: "tandem:a", Parent: "bench:hulk/alloc-7",
			Vector: jobs.Vector{"cpu": 3}}); err != nil {
			t.Fatalf("a child inside its parent's reservation: %v", err)
		}
		// The machine has four cores free, but the PARENT has one. A nested
		// scheduler that could see past its parent would be the second
		// independent count of the same cores.
		_, err := a.Grant(jobs.Request{ID: "tandem:b", Parent: "bench:hulk/alloc-7",
			Vector: jobs.Vector{"cpu": 2}})
		if err == nil {
			t.Fatal("a nested grant outgrew its parent's reservation")
		}
		ref := mustRefusal(t, err)
		if ref.Authority != "bench:hulk/alloc-7" {
			t.Errorf("refusal authority = %q, want the parent it draws from", ref.Authority)
		}
		if ref.Free != 1 {
			t.Errorf("refusal says free=%d, want 1: the parent's REMAINING capacity", ref.Free)
		}
		if !strings.Contains(err.Error(), "not from the machine") {
			t.Errorf("refusal %q does not say the budget is the parent's", err)
		}
		// A parent is not released while a grant drawn from it is live: that
		// would hand the same cores out twice.
		relErr := a.Release("bench:hulk/alloc-7")
		if relErr == nil {
			t.Fatal("released a parent while a nested grant was drawing from it")
		}
		if !strings.Contains(relErr.Error(), "tandem:a") {
			t.Errorf("the refusal %q does not name the nested grant holding it", relErr)
		}
		// Released to the PARENT, not to the machine: the freed core is the
		// parent's again and the second child now fits.
		if err := a.Release("tandem:a"); err != nil {
			t.Fatalf("Release(tandem:a): %v", err)
		}
		if _, err := a.Grant(jobs.Request{ID: "tandem:b", Parent: "bench:hulk/alloc-7",
			Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatalf("the parent's reservation did not take the release back: %v", err)
		}
		// The machine is still charged the parent's 4 and never the children's
		// on top of it: a grandchild's cores are inside its parent's already.
		if got, want := a.Snapshot().Line(), "JOBS live=2 lanes=- cpu=4/8 writes=0"; got != want {
			t.Errorf("status line = %q, want %q (the children are not charged twice)", got, want)
		}
	})

	// A7 jobs-intersecting-writes-serialize-across-lanes: an area of the tree
	// and a file are not the same grain. Two units in two lanes contend on
	// nothing a lane can see, and still cannot both write docs/SPEC-WORKLANG.md.
	t.Run("jobs-intersecting-writes-serialize-across-lanes", func(t *testing.T) {
		a := jobs.New(nil)
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "lanes:spec", Vector: jobs.Vector{jobs.Lane("docs"): 1},
			Writes: []string{"docs/SPEC-WORKLANG.md", "internal/worklang/"}}); err != nil {
			t.Fatalf("the first unit: %v", err)
		}
		// A different lane, a free vector, an intersecting write.
		_, err := a.Grant(jobs.Request{ID: "spec:amend", Vector: jobs.Vector{jobs.Lane("work"): 1},
			Writes: []string{"docs/SPEC-WORKLANG.md"}})
		if err == nil {
			t.Fatal("two units wrote one file because their lanes differed")
		}
		ref := mustRefusal(t, err)
		if ref.Path != "docs/SPEC-WORKLANG.md" || ref.Holder != "lanes:spec" {
			t.Errorf("refusal names path=%q holder=%q, want the file and who holds it", ref.Path, ref.Holder)
		}
		if !strings.Contains(err.Error(), "even when the lanes differ") {
			t.Errorf("refusal %q does not say why a different lane did not help", err)
		}
		// The grain is the path, not the string: a directory contains the file
		// under it, and a comparison by equality would let these run together.
		if _, err := a.Grant(jobs.Request{ID: "units:reader", Vector: jobs.Vector{jobs.Lane("ci"): 1},
			Writes: []string{"internal/worklang/units.go"}}); err == nil {
			t.Error("a unit wrote internal/worklang/units.go while another held internal/worklang/")
		}
		// Disjoint writes in a third lane are not held back by either of them.
		if _, err := a.Grant(jobs.Request{ID: "verb:hygiene", Vector: jobs.Vector{jobs.Lane("pulse"): 1},
			Writes: []string{"internal/pulse/hygiene.go"}}); err != nil {
			t.Fatalf("a unit with disjoint writes was held back: %v", err)
		}
		// And the writes are returned with the grant.
		if err := a.Release("lanes:spec"); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if _, err := a.Grant(jobs.Request{ID: "spec:amend", Vector: jobs.Vector{jobs.Lane("work"): 1},
			Writes: []string{"docs/SPEC-WORKLANG.md"}}); err != nil {
			t.Fatalf("the path was not returned by the release: %v", err)
		}
	})

	// A9 jobs-a-ready-unit-goes-with-no-global-barrier: a unit goes when its
	// OWN needs are closed and its OWN vector is free. There is no phase, no
	// round, no wave: a refusal ahead of it in the pass does not hold it, an
	// uncertain unit elsewhere does not hold it, and admission never parks a
	// caller to wait for either.
	t.Run("jobs-a-ready-unit-goes-with-no-global-barrier", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 4})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "merge:batch", Vector: jobs.Vector{jobs.Lane("merge"): 1, "cpu": 1}}); err != nil {
			t.Fatalf("the first unit: %v", err)
		}
		// This one is refused: the merge lane has capacity 1.
		blocked, err := a.Grant(jobs.Request{ID: "merge:queue",
			Vector: jobs.Vector{jobs.Lane("merge"): 1, "cpu": 1}})
		if err == nil {
			t.Fatalf("two live units in one lane: %+v", blocked)
		}
		// ...and the refusal is NOW, with the reason, rather than a wait. If
		// admission parked here the test would not reach the next line, which
		// is the whole of the proof: the barrier would be the deadlock.
		if ref := mustRefusal(t, err); ref.Dim != jobs.Lane("merge") {
			t.Errorf("refusal names %q, want the lane", ref.Dim)
		}
		// The unit AFTER the refused one goes in the same pass. Nothing about
		// merge:queue's state is a gate on pulse:certify.
		for _, req := range []jobs.Request{
			{ID: "pulse:certify", Vector: jobs.Vector{jobs.Lane("pulse"): 1, "cpu": 1}},
			{ID: "docs:seed", Vector: jobs.Vector{jobs.Lane("docs"): 1, "cpu": 1}},
			{ID: "bus:walk", Vector: jobs.Vector{jobs.Lane("bus"): 1, "cpu": 1}},
		} {
			if _, err := a.Grant(req); err != nil {
				t.Fatalf("%s waited on a unit it has nothing to do with: %v", req.ID, err)
			}
		}
		// Unrelated lanes scattered; the one contended lane held exactly one.
		snap := a.Snapshot()
		if got := strings.Join(snap.Lanes, ","); got != "bus,docs,merge,pulse" {
			t.Errorf("lanes held = %q, want bus,docs,merge,pulse", got)
		}
		if len(snap.Grants) != 4 {
			t.Errorf("live grants = %d, want 4", len(snap.Grants))
		}
		// A unit whose vector is free goes even while another unit is refused
		// on a dimension it does not name at all: cpu is now full, so a request
		// that asks for NO cpu is still granted. A slot count could not see the
		// difference; the vector can (A5).
		if _, err := a.Grant(jobs.Request{ID: "fetch:toolchain", Vector: jobs.Vector{jobs.Lane("sandbox"): 1, "network": 0}}); err != nil {
			t.Fatalf("a unit asking for no cpu was held by a full cpu: %v", err)
		}
		if _, err := a.Grant(jobs.Request{ID: "ci:shards", Vector: jobs.Vector{jobs.Lane("ci"): 1, "cpu": 1}}); err == nil {
			t.Error("cpu is 4/4 and a fifth core was granted")
		}
	})

	// A8 jobs-double-reservation-is-a-refusal-not-a-wait.
	t.Run("jobs-double-reservation-is-a-refusal-not-a-wait", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 8})
		defer a.Close()
		if _, err := a.Grant(jobs.Request{ID: "verb:fill", Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatal(err)
		}
		_, err := a.Grant(jobs.Request{ID: "verb:fill", Vector: jobs.Vector{"cpu": 2}})
		if err == nil {
			t.Fatal("one unit holds two reservations")
		}
		if !strings.Contains(err.Error(), "not a wait") {
			t.Errorf("refusal %q does not say it is a refusal rather than a wait", err)
		}
		if got := a.Snapshot(); len(got.Grants) != 1 {
			t.Errorf("live grants = %d, want 1", len(got.Grants))
		}
	})

	// A6 jobs-one-live-unit-per-lane and jobs-unrelated-lanes-scatter: within a
	// lane units are serial, across lanes they scatter and gather. A lane's
	// capacity is 1 and is never declared, because the number lives in A6.
	t.Run("jobs-one-live-unit-per-lane", func(t *testing.T) {
		a := jobs.New(nil)
		defer a.Close()
		if _, err := a.Grant(jobs.Request{ID: "a", Vector: jobs.Vector{jobs.Lane("pulse"): 1}}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Grant(jobs.Request{ID: "b", Vector: jobs.Vector{jobs.Lane("pulse"): 1}}); err == nil {
			t.Fatal("two live units in the pulse lane")
		}
		if err := a.Release("a"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Grant(jobs.Request{ID: "b", Vector: jobs.Vector{jobs.Lane("pulse"): 1}}); err != nil {
			t.Fatalf("the lane was not returned by the release: %v", err)
		}
	})

	t.Run("jobs-unrelated-lanes-scatter", func(t *testing.T) {
		a := jobs.New(nil)
		defer a.Close()
		for _, lane := range []string{"merge", "swarm", "pulse", "bus", "ci", "work", "decide", "docs", "sandbox"} {
			if _, err := a.Grant(jobs.Request{ID: lane + ":u", Vector: jobs.Vector{jobs.Lane(lane): 1}}); err != nil {
				t.Fatalf("lane %s did not scatter: %v", lane, err)
			}
		}
		if n := len(a.Snapshot().Lanes); n != 9 {
			t.Errorf("lanes held = %d, want all 9 of queue/control/lanes.tsv", n)
		}
	})

	// A5 jobs-a-download-asks-for-network-and-no-cpu: the vector's dimensions
	// are independent, which is the thing a slot count cannot express.
	t.Run("jobs-a-download-asks-for-network-and-no-cpu", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 2, "network": 1})
		defer a.Close()
		if _, err := a.Grant(jobs.Request{ID: "build", Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatal(err)
		}
		// Every core is taken; the download does not want one.
		if _, err := a.Grant(jobs.Request{ID: "download", Vector: jobs.Vector{"network": 1}}); err != nil {
			t.Fatalf("a download waited on cpu it never asked for: %v", err)
		}
		// A second download does contend, on the dimension it does name.
		if _, err := a.Grant(jobs.Request{ID: "download-2", Vector: jobs.Vector{"network": 1}}); err == nil {
			t.Error("two downloads over one uplink")
		}
	})

	// A dimension no authority counts is a refusal, not a silent grant: an
	// uncounted capacity is exactly the independent second counter A8 forbids.
	t.Run("jobs-an-uncounted-dimension-is-a-refusal", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 4})
		defer a.Close()
		_, err := a.Grant(jobs.Request{ID: "u", Vector: jobs.Vector{"gpu": 1}})
		if err == nil {
			t.Fatal("granted a capacity no authority counts")
		}
		if !strings.Contains(err.Error(), "gpu") {
			t.Errorf("refusal %q does not name the dimension", err)
		}
	})
}

func mustRefusal(t *testing.T, err error) *jobs.Refusal {
	t.Helper()
	ref, ok := err.(*jobs.Refusal)
	if !ok {
		t.Fatalf("error %v is not a *jobs.Refusal; admission refuses, it does not fail vaguely", err)
	}
	return ref
}
