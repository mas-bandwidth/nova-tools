package member

import (
	"bytes"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdsRunner is a fakeRunner that is a Holder: it records every set of cards it is told.
type holdsRunner struct {
	*fakeRunner
	mu   sync.Mutex
	held [][]Packet
}

func (r *holdsRunner) Holds(cards []Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held = append(r.held, slices.Clone(cards))
}

func (r *holdsRunner) told() [][]Packet {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.held)
}

// TestTheRunnerIsToldTheCardsTheQueueHoldsEachPass pins the one thing the pass hands the
// runner's sweep: every card of this member's columns, once a pass, as its packet when the
// answer carries one and as its claim (the answer's epoch, its gen or attempt, the loop's
// kind) when it does not; a pass whose queue did not answer tells nothing.
func TestTheRunnerIsToldTheCardsTheQueueHoldsEachPass(t *testing.T) {
	t.Parallel()
	for _, reader := range []bool{false, true} {
		t.Run(map[bool]string{false: "member", true: "reader"}[reader], func(t *testing.T) {
			t.Parallel()
			s, r := newScript(), &holdsRunner{fakeRunner: newRunner()}
			m := New(Config{As: "m", Width: 1, Reader: reader}, s, r, &fakePusher{}, &bytes.Buffer{})
			p := pk("c1")
			kind := "work"
			if reader {
				p = Packet{Card: "c1", Kind: "read", As: "m", Attempt: 2, Epoch: 13}
				kind = "read"
			}
			s.set("queue", 0, queueJSON(t, 13, working("c1", 1, &p), queueCard{ID: "c2", Col: "working", Gen: 3, Attempt: 2}))
			_, err := m.Tick(time.Unix(0, 0))
			require.NoError(t, err)
			s.set("queue", 0, queueJSON(t, 14))
			_, err = m.Tick(time.Unix(0, 0))
			require.NoError(t, err)
			s.set("queue", 1, "the store did not answer")
			_, err = m.Tick(time.Unix(0, 0))
			require.Error(t, err)
			assert.Equal(t, [][]Packet{
				{p, {Card: "c2", Kind: kind, Gen: 3, Attempt: 2, Epoch: 13}},
				{},
			}, r.told())
		})
	}
}
