package swarm

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// The `chore` alias has to reach the boundary a CARD is read at.
//
// Stella's r2 hold (#1925, e1198bab): `decide.ParseUnit` and the route flags
// canonicalize the alias, but a card is not JSON and does not go through
// either. `CardUnit` reads `KIND: chore` from the card's own header, or the
// word from its contract line, and handed it to `decide.KnownKind` unresolved
// -- so the card was untyped, `ok` was false, and it fell back to today's
// model with no rung and no route row. The alias is resolved ONCE, here, at
// the DECIDE read, before validation and before anything logs a kind.
//
// Both ingestion forms are controlled: the stated header and the phrase read
// from the contract line. `chore` is NOT added to the gate vocabulary -- it is
// a second name for a kind the table already holds.
func TestCardIngestionCanonicalizesTheChoreAliasInHeaderAndContract(t *testing.T) {
	t.Run("the card states KIND: chore itself", func(t *testing.T) {
		u, ok := CardUnit("card-h", "CONTRACT: bump the pinned toolchain on every bench",
			"KIND: chore\nFILES: 2\nPACKAGES: 1\nLANES: 1\nLANE: fleet\n")
		if !ok {
			t.Fatal("a card whose header says KIND: chore was not routable; the alias never reached the read")
		}
		if u.Kind != decide.KindFleetChore {
			t.Errorf("the ingested unit carries kind %q, want %q; the alias would reach the route log", u.Kind, decide.KindFleetChore)
		}
		if u.Files != 2 || u.Packages != 1 || u.Lanes != 1 {
			t.Errorf("the rest of the evidence was dropped: %+v", u)
		}
	})

	t.Run("the contract line says it and the header does not", func(t *testing.T) {
		u, ok := CardUnit("card-c", "CONTRACT: a chore to rotate the bench logs",
			"FILES: 1\nPACKAGES: 1\nLANES: 1\n")
		if !ok {
			t.Fatal("a card whose contract calls itself a chore was not routable")
		}
		if u.Kind != decide.KindFleetChore {
			t.Errorf("the contract-derived kind is %q, want %q", u.Kind, decide.KindFleetChore)
		}
	})

	t.Run("a near miss is still no card", func(t *testing.T) {
		for _, bad := range []string{"choree", "chores", "fleetchore"} {
			if _, ok := CardUnit("card-b", "CONTRACT: nothing typed here", "KIND: "+bad+"\nFILES: 1\n"); ok {
				t.Errorf("kind %q was accepted; only `chore` is an alias", bad)
			}
		}
	})

	t.Run("the alias steals no contract line another phrase already types", func(t *testing.T) {
		for _, tc := range []struct{ contract, want string }{
			{"CONTRACT: a chore to move the spec section", decide.KindSpec},
			{"CONTRACT: a chore on the sandbox guard", decide.KindGuard},
			{"CONTRACT: a chore to retarget the fixture", decide.KindFixtureRetarget},
		} {
			u, ok := CardUnit("card-p", tc.contract, "FILES: 1\nPACKAGES: 1\nLANES: 1\n")
			if !ok || u.Kind != tc.want {
				t.Errorf("%q typed as %q (ok=%v), want %q", tc.contract, u.Kind, ok, tc.want)
			}
		}
	})
}
