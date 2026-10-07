package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A card that reaches review is asked a read in the same tick when any reader
// it may be asked of has room; when none has, the tick records "waiting for a
// reader" on the card, once an attempt, and asks it the moment one frees. The
// stall and stranded judgments are left for an ask refused for a reason the
// coordinator decides (the night of 2026-10-05: three cards sat in review with
// no read asked and no note until the coordinator ran ask by hand, each after
// a stall judgment).

// tickAsk runs the readers' ask part the tick installs (friendAskPart around
// TickAsk) once, applies its plan, and returns it with its due count.
func tickAsk(t *testing.T, w *world, seats []FriendSeat) (Plan, int) {
	t.Helper()
	var fn TickPartFn
	for _, u := range TickTables {
		for _, part := range u.Parts {
			if u.Table == Readers && part.Name == "ask" {
				fn = part.Fn
			}
		}
	}
	require.NotNil(t, fn, "the readers ask part is not installed")
	p, due := fn(w.s, TickReq{Friends: seats})
	return w.must(p), due
}

// stalls is the no-stall rule's findings on the primary.
func stalls(w *world, id string) []Finding {
	var out []Finding
	for _, f := range Unheld(HeldState{Snap: w.s, Running: true}, w.s.Now) {
		if f.Subject == id {
			out = append(out, f)
		}
	}
	return out
}
