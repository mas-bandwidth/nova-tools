package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A member whose fresh beat says it starts no card (Snapshot.NoRoom) is out of the deal's up
// (notQuiet), and the deal's refusal names it with its word (quietWhy); a reader so is shown
// with its word in the readers' text the ask's refusals carry.
func TestNoRoomIsOutOfTheDealWithItsWord(t *testing.T) {
	t.Parallel()
	why := "free disk on the volume of /slots is 0.0 GiB, under the floor of 10 GiB"
	s := &Snapshot{NoRoom: map[string]string{"m1": why}}
	assert.Equal(t, []string{"m2"}, notQuiet(s, []string{"m1", "m2"}))
	assert.Equal(t, "starts no card: m1 ("+why+")", quietWhy(s, []string{"m1", "m2"}))
	assert.Empty(t, quietWhy(&Snapshot{}, []string{"m1", "m2"}))
	assert.Equal(t, []string{"m1", "m2"}, notQuiet(&Snapshot{}, []string{"m1", "m2"}))
}
