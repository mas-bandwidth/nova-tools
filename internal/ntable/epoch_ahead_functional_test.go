//go:build functional

package ntable_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// An ordinary write with an epoch ahead of the active one says what a batch
// says: the requested epoch is ahead, not stale.
func TestOrdinaryWriteWithAnEpochAheadIsAheadNotStale(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	_, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m", 1, ntable.WriteOptions{Epoch: 5})
	if !errors.Is(err, ntable.ErrEpochAhead) || errors.Is(err, ntable.ErrStale) || !strings.Contains(err.Error(), "ahead") {
		t.Errorf("an ordinary write at epoch 5, active 0: %v; want EPOCHAHEAD", err)
	}
}
