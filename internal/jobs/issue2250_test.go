package jobs

import (
	"strings"
	"testing"
)

func TestIssue2250(t *testing.T) {
	// jobs-done-when-is-a-report-not-a-gate: a work set's :done-when
	// describes the set's finish line as a report; a member whose own
	// needs are closed goes even while other members are open — the
	// set-level finish never idle-waits the fleet. Read the :done-when
	// value surfaced by the reader as a report only.
	t.Run("jobs-done-when-is-a-report-not-a-gate", func(t *testing.T) {
		g, err := SeedSet("all units accepted", []Node{
			{ID: "a", Needs: []string{"b"}},
			{ID: "b"}, // open PR
			{ID: "c"}, // no needs, ready to go
		})
		if err != nil {
			t.Fatalf("seed set: %v", err)
		}
		// done-when is surfaced as report-only metadata.
		if got := g.DoneWhen(); got != "all units accepted" {
			t.Errorf("DoneWhen() = %q, want the report", got)
		}
		// c's own needs are closed (none), so c is ready regardless of
		// what the set's done-when says. The done-when is a report, not
		// a gate that idle-waits the fleet.
		ready, blocker := g.Ready("c")
		if !ready {
			t.Fatalf("c is not ready; done-when gated a unit whose own needs are closed")
		}
		if blocker != nil {
			t.Fatalf("c's blocker = %v, want nil: c has no needs", blocker)
		}
		// a is still blocked on b (an open PR).
		ready, blocker = g.Ready("a")
		if ready {
			t.Fatal("a is ready while its need b is an open PR")
		}
		if blocker == nil || blocker.Need != "b" {
			t.Fatalf("a's blocker = %v, want b", blocker)
		}
		// The ready set includes b and c, not a. done-when doesn't
		// change this.
		if got := g.ReadySet(); !sameIDs(got, []string{"b", "c"}) {
			t.Fatalf("ready set = %v, want [b c]", got)
		}
	})

	// jobs-a-unit-without-acceptance-is-refused-at-load: once a set
	// names acceptance, a unit carrying none is refused at load (exit 2)
	// rather than scheduled; the refusal names the unit and the missing
	// :acceptance.
	t.Run("jobs-a-unit-without-acceptance-is-refused-at-load", func(t *testing.T) {
		_, err := SeedSet("all accepted", []Node{
			{ID: "a", Acceptance: []Acceptance{{ID: "a1", Kind: "test", Subject: "test:internal/jobs", Predicate: "passes"}}},
			{ID: "b"}, // no acceptance
		})
		if err == nil {
			t.Fatal("a unit without acceptance was not refused at load")
		}
		if !strings.Contains(err.Error(), "b") {
			t.Errorf("refusal %q does not name the unit", err)
		}
		if !strings.Contains(err.Error(), "acceptance") {
			t.Errorf("refusal %q does not name the missing acceptance", err)
		}
		// The unit with acceptance is fine on its own.
		_, err = SeedSet("all accepted", []Node{
			{ID: "a", Acceptance: []Acceptance{{ID: "a1", Kind: "test", Subject: "test:internal/jobs", Predicate: "passes"}}},
		})
		if err != nil {
			t.Fatalf("a unit with acceptance was refused: %v", err)
		}
		// No acceptance anywhere is fine (the gate is not armed).
		_, err = SeedSet("", []Node{
			{ID: "a"},
			{ID: "b"},
		})
		if err != nil {
			t.Fatalf("no acceptance anywhere was refused: %v", err)
		}
	})
}
