//go:build functional

package machine

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// The store tier of IT17: the round trips of a tick counted on a real store
// (E8's count, in the functional container), which the twin tests pin on the
// twin. They skip while Layer 2's fragment is absent (storetier_test.go), and
// once it lands they fail until gate G0 gives them the rest of their fixture:
// the sprint profile's library (fn.TSetSprint, which TSetSource refuses before
// G0) and the four tables defined on the store. The limit of IT17 (a busy tick
// at most three round trips, 2 MiB each way, 150 ms of store and Go time
// beyond them, p99 over ten minutes at 128 ms) is the owner's drive through E9
// and at a far bench store, not these tests'.

// requireSprintProfile fails a store-tier test while the sprint profile does
// not assemble: its body cannot run without it.
func requireSprintProfile(t *testing.T) {
	t.Helper()
	if _, err := fn.TSetSource(fn.TSetSprint); err != nil {
		t.Fatalf("the sprint profile does not assemble (owed to G0): %v", err)
	}
	t.Fatal("not implemented: the store tier's fixture (the sprint library loaded, the four tables defined on the store) waits on G0")
}

// TestTickStoreIdleOneRoundTrip: on the store, an idle tick is one round trip
// (1.4.2, T5; E8).
func TestTickStoreIdleOneRoundTrip(t *testing.T) {
	t.Parallel()
	requireTSetLogFragment(t)
	requireSprintProfile(t)
}

// TestTickStoreBusyAtMostThree: on the store, a busy tick is at most three
// round trips (1.4.2; E8).
func TestTickStoreBusyAtMostThree(t *testing.T) {
	t.Parallel()
	requireTSetLogFragment(t)
	requireSprintProfile(t)
}
