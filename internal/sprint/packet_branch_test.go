package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The branch an attempt is pushed to is unique per epoch: a card id comes back after a clear,
// and a branch named by the card alone was pass 1's, so every push of pass 2 was refused
// non-fast-forward (the quack sprint, 2026-10-01). The name carries the generation and the
// epoch as the slot and job names do: sprint/<prefix><card>.g<gen>.e<epoch>.
func TestTwoEpochsOfTheSameCardPushToTwoBranches(t *testing.T) {
	t.Parallel()
	work := &Card{ID: "quack-tools-01.w1", Row: "m1", Fields: map[string]string{"kind": "work", "primary": "quack-tools-01", "attempt": "1", "gen": "1"}}
	primary := &Card{ID: "quack-tools-01", Fields: map[string]string{"attempt": "1", "brief": "b"}}
	one, two := PacketOf("", 1, work, primary, nil, nil), PacketOf("", 2, work, primary, nil, nil)
	assert.Equal(t, "sprint/quack-tools-01.w1.g1.e1", one.Branch)
	assert.Equal(t, "sprint/quack-tools-01.w1.g1.e2", two.Branch)
	assert.NotEqual(t, one.Branch, two.Branch)

	// a rework names the attempt before's branch of the same epoch when no finish reported one,
	// and a read the work's
	again := &Card{ID: "quack-tools-01.w2", Row: "m1", Fields: map[string]string{"kind": "work", "primary": "quack-tools-01", "attempt": "2", "gen": "1"}}
	assert.Equal(t, "sprint/quack-tools-01.w1.g1.e2", PacketOf("", 2, again, primary, []*Card{work}, nil).Base)
	read := &Card{ID: "quack-tools-01.r1", Row: "reader-a", Fields: map[string]string{"kind": "read", "primary": "quack-tools-01", "attempt": "1"}}
	assert.Equal(t, "sprint/quack-tools-01.w1.g1.e2", PacketOf("", 2, read, primary, nil, work).WorkBranch)
}

// The branch is per launch, not per attempt: a card withdrawn from one member and dealt to
// another, or dealt again after a staging or provider failure, is another generation of the
// same attempt in the same epoch, and its push must not meet the first launch's (the quack
// sprint, pass 3, epoch 4: `! [rejected] sprint/quack-gates-03.w1.e4 (non-fast-forward)`).
func TestTwoLaunchesOfOneAttemptInOneEpochPushToTwoBranches(t *testing.T) {
	t.Parallel()
	primary := &Card{ID: "quack-gates-03", Fields: map[string]string{"attempt": "1", "brief": "b"}}
	launch := func(gen string) *Card {
		return &Card{ID: "quack-gates-03.w1", Row: "m1", Fields: map[string]string{"kind": "work", "primary": "quack-gates-03", "attempt": "1", "gen": gen}}
	}
	one, three := PacketOf("", 4, launch("1"), primary, nil, nil), PacketOf("", 4, launch("3"), primary, nil, nil)
	assert.Equal(t, "sprint/quack-gates-03.w1.g1.e4", one.Branch)
	assert.Equal(t, "sprint/quack-gates-03.w1.g3.e4", three.Branch)
	assert.NotEqual(t, one.Branch, three.Branch)
	assert.Equal(t, "sprint/p-quack-gates-03.w1.g3.e4", BranchOf("p-", 4, "quack-gates-03.w1", 3))

	// a read is handed the branch of the launch it reads
	read := &Card{ID: "quack-gates-03.r1", Row: "reader-a", Fields: map[string]string{"kind": "read", "primary": "quack-gates-03", "attempt": "1"}}
	assert.Equal(t, "sprint/quack-gates-03.w1.g3.e4", PacketOf("", 4, read, primary, nil, launch("3")).WorkBranch)
}
