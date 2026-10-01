package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The branch an attempt is pushed to is unique per epoch: a card id comes back after a clear,
// and a branch named by the card alone was pass 1's, so every push of pass 2 was refused
// non-fast-forward (the quack sprint, 2026-10-01). The name carries the epoch as the slot and
// job names do: sprint/<prefix><card>.e<epoch>.
func TestTwoEpochsOfTheSameCardPushToTwoBranches(t *testing.T) {
	t.Parallel()
	work := &Card{ID: "quack-tools-01.w1", Row: "m1", Fields: map[string]string{"kind": "work", "primary": "quack-tools-01", "attempt": "1", "gen": "1"}}
	primary := &Card{ID: "quack-tools-01", Fields: map[string]string{"attempt": "1", "brief": "b"}}
	one, two := PacketOf("", 1, work, primary, nil, nil), PacketOf("", 2, work, primary, nil, nil)
	assert.Equal(t, "sprint/quack-tools-01.w1.e1", one.Branch)
	assert.Equal(t, "sprint/quack-tools-01.w1.e2", two.Branch)
	assert.NotEqual(t, one.Branch, two.Branch)

	// a rework names the attempt before's branch of the same epoch when no finish reported one,
	// and a read the work's
	again := &Card{ID: "quack-tools-01.w2", Row: "m1", Fields: map[string]string{"kind": "work", "primary": "quack-tools-01", "attempt": "2", "gen": "1"}}
	assert.Equal(t, "sprint/quack-tools-01.w1.e2", PacketOf("", 2, again, primary, []*Card{work}, nil).Base)
	read := &Card{ID: "quack-tools-01.r1", Row: "reader-a", Fields: map[string]string{"kind": "read", "primary": "quack-tools-01", "attempt": "1"}}
	assert.Equal(t, "sprint/quack-tools-01.w1.e2", PacketOf("", 2, read, primary, nil, work).WorkBranch)
}
