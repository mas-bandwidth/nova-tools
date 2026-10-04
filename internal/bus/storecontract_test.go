//go:build functional

package bus

import (
	"testing"
	"time"
)

// TestGitStoreContract runs the Store contract on the git transport. It is functional
// because it makes repositories and pushes; the logic it pins is the bus's own, stated
// once in runStoreContract. The model is tla/BusCursor.tla.
func TestGitStoreContract(t *testing.T) {
	t.Parallel()
	hermetic(t)
	newStore := func(t *testing.T) Store {
		t.Helper()
		return NewGitStore(cloneBus(t, bareBus(t)), "origin", "main", 3, 10*time.Second)
	}
	runStoreContract(t, newStore)
}
