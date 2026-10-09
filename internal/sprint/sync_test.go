package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The drift is read off the merge table's properties, the minutes counted to the
// snapshot's clock; with none recorded it is zero, and a property that is no number
// counts 0.
func TestDevDriftOfReadsTheMergeTablesProperties(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	assert.Equal(t, DevDrift{}, DevDriftOf(w.s), "no sync recorded")
	_, _, ok := LastDevSync(w.s)
	assert.False(t, ok)

	at := w.s.Now
	w.s.Merge.SetProp(PropDevSyncAt, at.Format(time.RFC3339))
	w.s.Merge.SetProp(PropDevSyncSha, "def5678")
	w.s.Merge.SetProp(PropDevSyncBaseLacks, "0")
	w.s.Merge.SetProp(PropDevSyncDevLacks, "3")
	w.tick(7 * time.Minute)
	assert.Equal(t, DevDrift{BaseLacks: 0, DevLacks: 3, Minutes: 7, LastSync: at, LastSha: "def5678"}, DevDriftOf(w.s))

	w.s.Merge.SetProp(PropDevSyncDevLacks, "many")
	assert.Equal(t, 0, DevDriftOf(w.s).DevLacks)
}
