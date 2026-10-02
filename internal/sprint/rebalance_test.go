package sprint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rebalance at the start of every tick (TickStart; the owner, 2026-10-01:
// "both for readers and fleet, there needs to be a rebalance step done at the
// start of each tick. it's simple. just once before tick, rebalance each
// table." and "and it's a safety, if ever there are cards on a held or down
// machine, rebalance moves them away.").

// heldCards is the work cards on a member, ready and working.
func heldCards(s *Snapshot, m string) int { return s.Fleet.Count(m, Ready) + s.Fleet.Count(m, Working) }

// A member that cannot start its ready cards (its lanes full) beside a member
// with free lanes and nothing ready: the level gives the idle member the
// cards, within one of each other in backlog, none past DealAhead times a
// width.
func TestTheLevelFeedsAnIdleMemberFromOneThatCannotStartItsCards(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 0, 2, "m1", "m2")
	for i := 1; i <= 2; i++ {
		putWorkCard(w, fmt.Sprintf("w%d", i), "m1", Working, float64(i), nil)
	}
	for i := 3; i <= 4; i++ {
		putWorkCard(w, fmt.Sprintf("w%d", i), "m1", Ready, float64(i), nil)
	}
	w.part(TickLevel, TickReq{})
	assert.Equal(t, 2, w.s.Fleet.Count("m2", Ready), "m2, idle, has m1's two ready cards m1 cannot start")
	assert.Equal(t, 0, w.s.Fleet.Count("m1", Ready))
	assert.Equal(t, 2, w.s.Fleet.Count("m1", Working), "working cards on an up member stay")
	for _, m := range []string{"m1", "m2"} {
		assert.LessOrEqual(t, heldCards(w.s, m), DealAhead*2, m)
	}
}

// The safety: cards planted on a held member and on a down member, ready and
// working, are on the members up after the one rebalance, a working card
// dealt again at a new generation with its redeal counted, none past DealAhead
// times a width.
func TestTheRebalanceMovesEveryCardOffAHeldOrDownMember(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"hold", "down"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			w := fleetWorld(t, 0, 4, "m1", "m2", "m3")
			w.must(FleetStep(w.s, FleetReq{Op: op, Member: "m1"}))
			require.NotEqual(t, Up, w.s.MemberCtl("m1").F("status"))
			putWorkCard(w, "a", "m1", Ready, 1, nil)
			putWorkCard(w, "b", "m1", Working, 2, nil)
			w.part(TickLevel, TickReq{})
			assert.Equal(t, 0, heldCards(w.s, "m1"), "no card is left on a %s member", op)
			for _, id := range []string{"a.w1", "b.w1"} {
				c := w.s.Fleet.Card(id)
				require.NotNil(t, c)
				assert.Contains(t, []string{"m2", "m3"}, c.Row, id)
				assert.Equal(t, Ready, c.Col, id)
				assert.Equal(t, 3, c.Int("gen"), "%s dealt again at a new generation", id)
			}
			assert.Equal(t, 1, w.s.Fleet.Card("b.w1").Int("redeals"), "the working card's take ended: its redeal counts")
			for _, m := range []string{"m2", "m3"} {
				assert.LessOrEqual(t, heldCards(w.s, m), DealAhead*4, m)
			}
		})
	}
}

// With no member up, the cards on a down member go back: withdrawn, their
// primaries ready again for the deal.
func TestTheRebalanceWithdrawsTheCardsOfADownMemberWhenNoneIsUp(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 1, 4, "m1") // one primary: the stream s1 has a row
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	putWorkCard(w, "a", "m1", Ready, 1, nil)
	putWorkCard(w, "b", "m1", Working, 2, nil)
	w.part(TickLevel, TickReq{})
	assert.Equal(t, 0, heldCards(w.s, "m1"))
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("a.w1").Col)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("b.w1").Col)
	assert.Equal(t, Ready, w.state("b"), "the working card's primary is ready again")
}

// readsWorld is readers r1..r8 up and primaries in review, each asked of two
// of r1, r2 and r3 (the measured case: the readers there when the reads were
// asked hold them all, the readers added since hold none).
func readsWorld(t *testing.T, primaries int) *world {
	t.Helper()
	var readers []string
	for i := 1; i <= 8; i++ {
		readers = append(readers, fmt.Sprintf("r%d", i))
	}
	w := newWorld(t, readers...)
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s1"))
	w.s.ReaderStates = map[string]string{}
	for _, r := range readers {
		w.s.ReaderStates[r] = ReaderUp
	}
	pairs := [][2]string{{"r1", "r2"}, {"r2", "r3"}, {"r1", "r3"}}
	for i := 1; i <= primaries; i++ {
		p := fmt.Sprintf("p%d", i)
		w.s.Work.Put(&Card{ID: p, Row: "s1", Col: Review, Score: float64(i), Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"}})
		for _, r := range pairs[i%3] {
			w.s.Readers.Put(&Card{ID: ReadCardID(p, 1, r), Row: r, Col: Asked, Score: float64(i), Rev: 1,
				Fields: map[string]string{"kind": "read", "primary": p, "stream": "s1", "reader": r, "attempt": "1"}})
		}
	}
	return w
}

// Three readers hold every asked read beside five idle ones: one rebalance
// evens the loads, each primary's reads stay on two different readers, and no
// reader holds a read of an attempt it already had.
func TestTheReadersRebalanceFeedsTheIdleReaders(t *testing.T) {
	t.Parallel()
	w := readsWorld(t, 24)
	w.part(TickLevelReads, TickReq{})
	lo, hi := -1, 0
	for _, r := range w.s.Readers.Rows() {
		n := readerLoad(w.s, r)
		if lo < 0 || n < lo {
			lo = n
		}
		hi = max(hi, n)
	}
	assert.LessOrEqual(t, hi-lo, 1, "the loads differ by %d after the rebalance", hi-lo)
	assert.Positive(t, lo, "no reader up is idle")
	for i := 1; i <= 24; i++ {
		p := fmt.Sprintf("p%d", i)
		seen := map[string]bool{}
		live := 0
		for _, c := range w.s.Readers.Of(p) {
			require.False(t, seen[c.Row], "%s has two cards on %s", p, c.Row)
			seen[c.Row] = true
			if c.Placed() {
				live++
			}
		}
		assert.Equal(t, 2, live, "%s keeps two reads", p)
	}
}

// A reader that is not up holds no read after the rebalance: its asked and
// reading reads are taken back, and the tick's ask asks them of the readers
// up, never of the one that had the attempt.
func TestTheReadersRebalanceTakesTheReadsOfAReaderAway(t *testing.T) {
	t.Parallel()
	w := readsWorld(t, 3)
	w.must(Read(w.s, ReadReq{As: "r1", Begin: true, Sel: Sel{IDs: []string{ReadCardID("p3", 1, "r1")}}}))
	w.s.ReaderStates["r1"] = ReaderAway
	w.part(TickLevelReads, TickReq{})
	assert.Equal(t, 0, readerLoad(w.s, "r1"), "the reader away holds nothing")
	w.part(TickAsk, TickReq{})
	for i := 1; i <= 3; i++ {
		p := fmt.Sprintf("p%d", i)
		assert.Len(t, liveReadsAt(w.s, w.s.Work.Card(p), 1), 2, "%s is read by two readers up", p)
		if rc := w.s.Readers.Card(ReadCardID(p, 1, "r1")); rc != nil {
			assert.False(t, rc.Placed(), "%s's read on r1 is retired", p)
		}
	}
}

// A read returned by its reader and then moved by the rebalance is a fresh ask
// on the reader it goes to: not returned, reasked 0, no run of the reader it
// left (no read_take_<n>, no usage); the card it leaves is retired with what it
// had.
func TestAReturnedReadMovedByTheRebalanceIsAFreshAsk(t *testing.T) {
	t.Parallel()
	w := readsWorld(t, 24)
	from := ReadCardID("p24", 1, "r1") // r1's newest: the first the rebalance moves
	w.must(Read(w.s, ReadReq{As: "r1", Begin: true, Sel: Sel{IDs: []string{from}}}))
	w.must(Read(w.s, ReadReq{As: "r1", Return: true, Reason: "no verdict", Usage: "input=10", Sel: Sel{IDs: []string{from}}}))
	left := w.s.Readers.Card(from)
	require.True(t, left.Placed(), "a return puts the read back in asked on its reader")
	require.Equal(t, 1, left.Int(FieldReasked))
	w.part(TickLevelReads, TickReq{})
	left = w.s.Readers.Card(from)
	require.False(t, left.Placed(), "the rebalance moved it")
	assert.Equal(t, RetiredByLevel, left.F("retired_by"))
	assert.Equal(t, 1, left.Int(FieldReasked), "the card it left keeps what it had")
	hadTake := false
	for k := range left.Fields {
		hadTake = hadTake || strings.HasPrefix(k, FieldReadTake)
	}
	assert.True(t, hadTake, "the returned run's record stays on the card it left")
	var moved *Card
	for _, c := range w.s.Readers.Of("p24") {
		if c.Placed() && c.Row != "r1" && c.Row != "r2" {
			moved = c
		}
	}
	require.NotNil(t, moved, "p24's read is on another reader")
	assert.Equal(t, Asked, moved.Col)
	assert.Equal(t, 0, moved.Int(FieldReasked), "a fresh ask: the new reader's bound starts at zero")
	assert.Empty(t, moved.F(FieldReturned))
	assert.Empty(t, moved.F(FieldUsage))
	for k := range moved.Fields {
		assert.False(t, strings.HasPrefix(k, FieldReadTake), "no run of the reader it left: %s", k)
	}
}
