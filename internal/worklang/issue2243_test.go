package worklang

import (
	"strings"
	"testing"
)

// TestIssue2243 exercises the two spec behaviours nova-tools#2243 covers:
// behaviour 37 -- a :collects collection is named before the run and its
// members are bound to the unit's revision only when harvested;
// behaviour 40 -- a unit with no :acceptance is refused at load (exit 2).
func TestIssue2243(t *testing.T) {
	t.Parallel()

	// jobs-a-collection-binds-its-members-at-harvest: a :collects entry is
	// named before the run and its members are bound to the unit's revision
	// only when harvested, not before.
	t.Run("jobs-a-collection-binds-its-members-at-harvest", func(t *testing.T) {
		src := []byte(`(work-set "s" :units ((unit "u1"
			:collects ((:name "receipts" :under "out/receipts" :members :unknown-before-run))
			:acceptance ((:id "a1" :kind :test :subject "t" :predicate :passes))
			:title "binds at harvest")))`)
		ws, err := ParseWorkSet("f.lisp", src, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if len(ws.Units) != 1 {
			t.Fatalf("got %d units", len(ws.Units))
		}
		u := ws.Units[0]
		cols := u.Collections()
		if len(cols) != 1 {
			t.Fatalf("got %d collections, want 1", len(cols))
		}
		c := &cols[0]
		// Before harvest: members are unknown and no revision is set.
		if !c.MembersUnknown {
			t.Error("MembersUnknown should be true before harvest")
		}
		if c.Revision != "" {
			t.Errorf("Revision should be empty before harvest, got %q", c.Revision)
		}
		if len(c.Members) != 0 {
			t.Errorf("Members should be empty before harvest, got %v", c.Members)
		}
		// Bind at harvest.
		c.BindMembers("abc1234", []string{"out/receipts/r1.txt", "out/receipts/r2.txt"})
		// After harvest: revision and members are set, unknown is cleared.
		if c.Revision != "abc1234" {
			t.Errorf("Revision = %q, want abc1234", c.Revision)
		}
		if len(c.Members) != 2 {
			t.Errorf("Members = %v, want 2 entries", c.Members)
		}
		if c.MembersUnknown {
			t.Error("MembersUnknown should be false after harvest")
		}
	})

	// jobs-a-unit-without-acceptance-is-refused-at-load: a unit with no
	// :acceptance is refused at load (exit 2) naming what it must name.
	t.Run("jobs-a-unit-without-acceptance-is-refused-at-load", func(t *testing.T) {
		src := []byte(`(work-set "s" :units (
			(unit "u1" :acceptance ((:id "a1" :kind :test :subject "t" :predicate :passes)))
			(unit "u2" :title "no acceptance here")))`)
		ws, err := ParseWorkSet("f.lisp", src, DefaultLimits())
		if err != nil {
			t.Fatalf("ParseWorkSet: %v", err)
		}
		err = ws.LoadCheck()
		if err == nil {
			t.Fatal("a unit without :acceptance was loaded instead of refused")
		}
		ref, ok := err.(*Refusal)
		if !ok {
			t.Fatalf("want *Refusal, got %T: %v", err, err)
		}
		if ref.ExitCode() != 2 {
			t.Errorf("exit = %d, want 2", ref.ExitCode())
		}
		msg := ref.Error()
		if !strings.Contains(msg, "u2") {
			t.Errorf("refusal %q does not name the unit without acceptance", msg)
		}
		if !strings.Contains(msg, "acceptance") {
			t.Errorf("refusal %q does not name acceptance", msg)
		}
	})
}
