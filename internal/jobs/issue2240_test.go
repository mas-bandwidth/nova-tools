package jobs_test

// Three boundary contracts of docs/SPEC-JOBS.md section 9's executor seam:
// the grant drawn from the parent, the result bound to the unit's revision and
// :acceptance criteria, and the uncertain state kept (not released) when an
// engine disconnects without termination proof. The seam stays a proposal -- no
// concrete engine is grown, only the typed boundary.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

func TestIssue2240(t *testing.T) {
	// jobs-an-executor-draws-its-budget-from-the-parent-grant: an external
	// executor's sub-budget comes out of the parent unit's reservation and
	// returns to it on release.
	t.Run("jobs-an-executor-draws-its-budget-from-the-parent-grant", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 8})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "unit:parent", Vector: jobs.Vector{"cpu": 4}}); err != nil {
			t.Fatalf("parent grant: %v", err)
		}

		exec, err := a.AdmitExecutor(jobs.Executor{ID: "exec:a", Parent: "unit:parent", Vector: jobs.Vector{"cpu": 2}})
		if err != nil {
			t.Fatalf("admit executor: %v", err)
		}
		if exec.ID != "exec:a" || exec.Parent != "unit:parent" {
			t.Errorf("executor grant = %+v, want id=exec:a parent=unit:parent", exec)
		}

		// The machine has four cores free, but the PARENT has two left. A
		// second executor must not see past that reservation.
		_, err = a.AdmitExecutor(jobs.Executor{ID: "exec:b", Parent: "unit:parent", Vector: jobs.Vector{"cpu": 3}})
		if err == nil {
			t.Fatal("executor outgrew its parent's remaining reservation")
		}
		ref := mustRefusal(t, err)
		if ref.Authority != "unit:parent" {
			t.Errorf("refusal authority = %q, want unit:parent", ref.Authority)
		}
		if ref.Free != 2 {
			t.Errorf("refusal free = %d, want 2 (parent's remaining)", ref.Free)
		}

		// Releasing the executor returns the budget to the parent, not to the
		// machine: the second executor now fits.
		if err := a.ReleaseExecutor("exec:a"); err != nil {
			t.Fatalf("release executor: %v", err)
		}
		if _, err := a.AdmitExecutor(jobs.Executor{ID: "exec:b", Parent: "unit:parent", Vector: jobs.Vector{"cpu": 3}}); err != nil {
			t.Fatalf("parent reservation did not take the release back: %v", err)
		}
		if got, want := a.Snapshot().Line(), "JOBS live=2 lanes=- cpu=4/8 writes=0"; got != want {
			t.Errorf("status line = %q, want %q (children are not charged twice)", got, want)
		}
	})

	// jobs-an-executor-result-binds-to-the-units-revision: an outcome carried
	// back across the seam must name the unit's revision and :acceptance
	// criteria; a mismatch is refused.
	t.Run("jobs-an-executor-result-binds-to-the-units-revision", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 8})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{
			ID:         "unit:bound",
			Vector:     jobs.Vector{"cpu": 4},
			Revision:   "abc123",
			Acceptance: []string{"unit-test", "lint"},
		}); err != nil {
			t.Fatalf("parent grant: %v", err)
		}
		if _, err := a.AdmitExecutor(jobs.Executor{ID: "exec:bound", Parent: "unit:bound", Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatalf("admit executor: %v", err)
		}

		// A matching outcome is accepted.
		if err := a.Report(jobs.Outcome{
			UnitID:     "unit:bound",
			ExecutorID: "exec:bound",
			Revision:   "abc123",
			Acceptance: []string{"unit-test", "lint"},
			Result:     "pass",
			Proof:      "exit 0 reaped",
		}); err != nil {
			t.Fatalf("report matching outcome: %v", err)
		}

		// A completion without termination proof is refused: an empty result
		// with Uncertain=false is not a finished unit.
		for _, o := range []jobs.Outcome{
			{UnitID: "unit:bound", ExecutorID: "exec:bound", Revision: "abc123", Acceptance: []string{"unit-test", "lint"}, Result: "pass"},
			{UnitID: "unit:bound", ExecutorID: "exec:bound", Revision: "abc123", Acceptance: []string{"unit-test", "lint"}},
		} {
			err := a.Report(o)
			if err == nil {
				t.Fatalf("accepted completion without termination proof: %+v", o)
			}
			if !strings.Contains(err.Error(), "termination proof") {
				t.Errorf("refusal %q does not name the missing termination proof", err)
			}
		}
		// Proof without a result is not a completion either.
		if err := a.Report(jobs.Outcome{UnitID: "unit:bound", ExecutorID: "exec:bound", Revision: "abc123",
			Acceptance: []string{"unit-test", "lint"}, Proof: "exit 0 reaped"}); err == nil {
			t.Fatal("accepted completion with proof but no result")
		}

		// A wrong revision is refused.
		err := a.Report(jobs.Outcome{
			UnitID:     "unit:bound",
			ExecutorID: "exec:bound",
			Revision:   "wrong",
			Acceptance: []string{"unit-test", "lint"},
			Result:     "pass",
			Proof:      "exit 0 reaped",
		})
		if err == nil {
			t.Fatal("accepted outcome with wrong revision")
		}
		if !strings.Contains(err.Error(), "revision") {
			t.Errorf("refusal %q does not name the revision mismatch", err)
		}

		// A wrong acceptance set is refused.
		err = a.Report(jobs.Outcome{
			UnitID:     "unit:bound",
			ExecutorID: "exec:bound",
			Revision:   "abc123",
			Acceptance: []string{"unit-test"},
			Result:     "pass",
			Proof:      "exit 0 reaped",
		})
		if err == nil {
			t.Fatal("accepted outcome with wrong acceptance criteria")
		}
		if !strings.Contains(err.Error(), "acceptance") {
			t.Errorf("refusal %q does not name the acceptance mismatch", err)
		}
	})

	// jobs-a-disconnected-executor-is-uncertain-not-stopped: without
	// termination proof from the engine, the unit is uncertain and the
	// executor's reservation is kept, never silently returned.
	t.Run("jobs-a-disconnected-executor-is-uncertain-not-stopped", func(t *testing.T) {
		a := jobs.New(jobs.Vector{"cpu": 8})
		defer a.Close()

		if _, err := a.Grant(jobs.Request{ID: "unit:live", Vector: jobs.Vector{"cpu": 4}}); err != nil {
			t.Fatalf("parent grant: %v", err)
		}
		if _, err := a.AdmitExecutor(jobs.Executor{ID: "exec:live", Parent: "unit:live", Vector: jobs.Vector{"cpu": 2}}); err != nil {
			t.Fatalf("admit executor: %v", err)
		}

		// The engine disconnects; there is no result, only uncertainty.
		if err := a.Report(jobs.Outcome{
			UnitID:     "unit:live",
			ExecutorID: "exec:live",
			Uncertain:  true,
		}); err != nil {
			t.Fatalf("report uncertain: %v", err)
		}

		// The executor's grant is still live.
		if _, held := a.Held("exec:live"); !held {
			t.Fatal("uncertain executor released its reservation")
		}

		// The unit is marked uncertain, not stopped.
		if !a.IsUncertain("exec:live") {
			t.Fatal("disconnected executor is not marked uncertain")
		}

		// The parent cannot be released while the uncertain child still draws
		// from it: that would hand the same capacity out twice.
		err := a.Release("unit:live")
		if err == nil {
			t.Fatal("released parent while uncertain executor still holds reservation")
		}
		if !strings.Contains(err.Error(), "exec:live") {
			t.Errorf("parent release refusal %q does not name the nested grant", err)
		}
	})
}
