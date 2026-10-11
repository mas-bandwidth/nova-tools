//go:build functional

package ntable_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
)

// An ordinary write with an epoch ahead of the active one says what a batch
// says: the requested epoch is ahead, not stale.
func TestOrdinaryWriteWithAnEpochAheadIsAheadNotStale(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	_, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m", 1, ntable.WriteOptions{Epoch: 5})
	assert.ErrorIs(t, err, ntable.ErrEpochAhead, "an ordinary write at epoch 5, active 0")
	assert.NotErrorIs(t, err, ntable.ErrStale, "an ordinary write at epoch 5, active 0")
	assert.ErrorContains(t, err, "ahead", "an ordinary write at epoch 5, active 0")
}
