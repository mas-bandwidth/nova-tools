package taskcard_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestFreeSlotsShrinkByCILegs (nova-tools#4293): a consumer's free slots
// are its slots minus the CI legs running minus the copies working; four
// legs on an eight-slot bench with one copy working leave three; legs past
// the slots leave none, never a negative fill.
func TestFreeSlotsShrinkByCILegs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		slots, working, ci int64
		want               int
	}{
		{8, 0, 0, 8},
		{8, 1, 0, 7},
		{8, 1, 4, 3},
		{8, 0, 8, 0},
		{8, 2, 9, 0},
		{0, 0, 0, 0},
	}
	for _, c := range cases {
		if got := taskcard.FreeSlots(c.slots, c.working, c.ci); got != c.want {
			t.Errorf("FreeSlots(%d, %d, %d) = %d, want %d", c.slots, c.working, c.ci, got, c.want)
		}
	}
}
