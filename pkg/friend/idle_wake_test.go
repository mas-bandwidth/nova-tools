package friend

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idleRig is the daemon rig on a clock that moves one minute per step, with the session's
// newest write and the cards she holds set by the test: the wake turns are what the fake
// harness was handed with "you hold" in them, the notes what the coordinator got. It runs in
// a synctest bubble: the harness answers at once and Pause waits until the turn's result is
// in, so a turn started at one step has ended by the next.
type idleRig struct {
	*rig
	active time.Time
	cards  []string
}

func newIdleRig(t *testing.T, cards ...string) *idleRig {
	t.Helper()
	r := &idleRig{rig: newRig(t), active: t0, cards: cards}
	r.now = t0.Add(-time.Minute) // the daemon starts at t0
	r.d.Coordinator = "ada"
	r.d.Now = func() time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.now = r.now.Add(time.Minute)
		return r.now
	}
	r.d.Activity = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.active }
	r.d.Cards = func() []string { r.mu.Lock(); defer r.mu.Unlock(); return r.cards }
	r.d.Deliver, r.passive = r, true
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	return r
}

func (r *idleRig) Deliver(_ context.Context, text string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delivered = append(r.delivered, text)
	return 0, nil
}

func (r *idleRig) wakes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, text := range r.delivered {
		if strings.Contains(text, "you hold") {
			out = append(out, text)
		}
	}
	return out
}

func (r *idleRig) notes(t *testing.T) []bus.Message {
	t.Helper()
	got, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 100)
	require.NoError(t, err)
	var out []bus.Message
	for _, e := range got {
		if m := e.Message(); strings.Contains(m.Subject, "idle") {
			out = append(out, m)
		}
	}
	return out
}

// The rule of docs/SPEC-FRIEND.md, idle wake: IdleAfter without a write while she holds cards
// gives one wake turn, IdleAfter more one blocker note to the coordinator, and a write resets
// both. A step is one minute; step n runs at t0+n minutes, and what the test checks at step n
// (from the beat) is what steps before n did.
func TestAnIdleFriendWithCardsGetsAWakeTurnThenANote(t *testing.T) {
	t.Parallel()
	t.Run("ten idle minutes give one wake turn, twenty one note, and then nothing more", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newIdleRig(t, "c-old", "c-new")
			r.at[9] = func() {
				assert.Empty(t, r.wakes(), "nine idle minutes are not ten")
			}
			r.at[11] = func() {
				assert.Len(t, r.wakes(), 1, "ten idle minutes gave the wake turn")
			}
			r.at[20] = func() {
				assert.Empty(t, r.notes(t), "the note waits a second IdleAfter after the wake")
			}
			r.run(t, 45)
			wakes := r.wakes()
			require.Len(t, wakes, 1, "one wake turn, however long she stays idle")
			assert.Contains(t, wakes[0], "you hold 2 cards")
			assert.Contains(t, wakes[0], "continue the oldest, c-old")
			notes := r.notes(t)
			require.Len(t, notes, 1, "one note, however long she stays idle")
			assert.Equal(t, bus.KindBlocker, notes[0].Kind)
			assert.Equal(t, "bob", notes[0].From)
			assert.Contains(t, notes[0].Subject, "friend bob")
			assert.Contains(t, notes[0].Subject, "c-old, c-new")
		})
	})
	t.Run("a write resets both: the next idle stretch gets its own wake and note", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newIdleRig(t, "c1")
			r.at[25] = func() { r.mu.Lock(); r.active = t0.Add(25 * time.Minute); r.mu.Unlock() }
			r.at[34] = func() {
				assert.Len(t, r.wakes(), 1, "nine minutes after the write is not idle")
			}
			r.run(t, 46)
			assert.Len(t, r.wakes(), 2, "a wake at minute 10 and another at minute 35")
			assert.Len(t, r.notes(t), 2, "a note at minute 20 and another at minute 45")
		})
	})
	t.Run("a write after the wake answers it: no note", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newIdleRig(t, "c1")
			r.at[12] = func() { r.mu.Lock(); r.active = t0.Add(12 * time.Minute); r.mu.Unlock() }
			r.run(t, 21)
			assert.Len(t, r.wakes(), 1)
			assert.Empty(t, r.notes(t), "the session moved after the wake")
		})
	})
	t.Run("no cards, no wake", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newIdleRig(t)
			r.run(t, 30)
			assert.Empty(t, r.wakes())
			assert.Empty(t, r.notes(t))
		})
	})
	t.Run("the row's setting moves both", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newIdleRig(t, "c1")
			r.d.IdleAfter = func() time.Duration { return 5 * time.Minute }
			r.at[4] = func() { assert.Empty(t, r.wakes()) }
			r.run(t, 11)
			assert.Len(t, r.wakes(), 1, "a wake at minute 5")
			assert.Len(t, r.notes(t), 1, "a note at minute 10")
		})
	})
}
