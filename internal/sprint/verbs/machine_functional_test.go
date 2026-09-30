//go:build functional

package verbs

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// The store tier of the machine's verbs: init, start, stop and clear through
// sprintfn.Redis on a store of the test's own, in the container
// (tools/functionalrun), counted by round trips as on the twin.
//
// It skips for one cause only: Layer 2's fragment lua/table_set_log.lua is not
// in the tree, so the composed library, which the sprint profile extends,
// cannot be assembled (missingTSetLogFragment). Once the fragment lands it
// fails, naming what it still needs, until gate G0 lets the sprint profile
// assemble and load and Layer 1's lifecycle defines the four tables on a
// store; it never passes by doing nothing.
func TestMachineVerbsOnAStore(t *testing.T) {
	t.Parallel()
	if _, err := fn.TSetSource(fn.TSetComposed); err != nil {
		if missingTSetLogFragment(err) {
			t.Skip(missingLogReason)
		}
		t.Fatalf("assemble the composed tset source: %v", err)
	}
	if _, err := fn.TSetSource(fn.TSetSprint); err != nil {
		t.Fatalf("the sprint profile does not assemble (%v): gate G0 owes it before this tier runs", err)
	}
	t.Fatal("owed at G0: the store fixture (Layer 1's table definitions on a store, the sprint profile loaded), then Init, Start, Stop and Clear through sprintfn.Redis at two round trips each")
}
