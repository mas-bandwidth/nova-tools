package sprint

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reader's width is its machine's fleet row's (the owner, 2026-10-03: with
// widths 4/8/16/16/24 the count-levelled reads queued seven deep on the two
// narrow machines while 31 reader slots sat idle), and the ask and the level
// place reads by room, free room as a share of width, never by count alone.

// widthWorld is readers reader-m1 and reader-m2, up, named for members m1 and
// m2 of the widths given, and n flash primaries in review, unasked.
func widthWorld(t *testing.T, w1, w2, primaries int) *world {
	t.Helper()
	w := newWorld(t, "reader-m1", "reader-m2")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: w1}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: w2}))
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s1"))
	w.s.ReaderStates = map[string]string{"reader-m1": ReaderUp, "reader-m2": ReaderUp}
	for i := 1; i <= primaries; i++ {
		p := fmt.Sprintf("p%d", i)
		w.s.Work.Put(&Card{ID: p, Row: "s1", Col: Review, Score: float64(i), Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"}})
	}
	return w
}

func TestAReadersWidthIsItsMachinesFleetRows(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 4, 24, 0)
	assert.Equal(t, 4, w.s.ReaderWidth("reader-m1"))
	assert.Equal(t, 24, w.s.ReaderWidth("reader-m2"))
	assert.Equal(t, math.MaxInt, w.s.ReaderWidth("reader-x"), "a reader named for no fleet row is unbounded")
	assert.Equal(t, math.MaxInt, w.s.ReaderWidth("r1"), "a reader not named for a machine is unbounded")
	w.s.Fleet = nil
	assert.Equal(t, math.MaxInt, w.s.ReaderWidth("reader-m1"), "a snapshot with no fleet table bounds no reader")
}

// Widths 4 and 24 with ten reads: the 24 takes eight, the 4 two, each filled
// to the same share of its width; by count the ten would be split five and
// five, with the 4 queued one deep past its lanes while the 24 sat with
// nineteen idle.
func TestTheAskGivesReadsByShareOfWidth(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 4, 24, 10)
	w.part(TickAsk, TickReq{})
	assert.Equal(t, 2, w.s.readerLoad("reader-m1"))
	assert.Equal(t, 8, w.s.readerLoad("reader-m2"))
	// the ask's placement is the level's fixed point: nothing moves back
	w.part(TickLevelReads, TickReq{})
	assert.Equal(t, 2, w.s.readerLoad("reader-m1"))
	assert.Equal(t, 8, w.s.readerLoad("reader-m2"))
}

// A reader at width is given nothing: with the 4 full, the reads go to the
// other reader, and a read no reader has room for waits, due, with no
// judgment.
func TestAReaderAtWidthIsAskedNothing(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 2, 3, 6)
	w.part(TickAsk, TickReq{})
	assert.Equal(t, 2, w.s.readerLoad("reader-m1"))
	assert.Equal(t, 3, w.s.readerLoad("reader-m2"))
	p, due := TickAsk(w.s, TickReq{})
	assert.Empty(t, p.Units, "both at width: nothing is asked")
	assert.Equal(t, 1, due, "the sixth read waits for room")
	assert.Empty(t, p.Notes, "waiting for room is no judgment")
	w.s.Readers.Card(ReadCardID("p1", 1, "reader-m1")).Col = OK
	w.s.Readers.cells = nil
	w.part(TickAsk, TickReq{})
	assert.Equal(t, 2, w.s.readerLoad("reader-m1"), "the lane freed is asked again")
	assert.Equal(t, 6, w.s.Readers.Count("reader-m1", Asked)+w.s.Readers.Count("reader-m1", Reading)+w.s.Readers.Count("reader-m1", OK)+w.s.readerLoad("reader-m2"))
}

// A reader named for no fleet row is unbounded: it is never at width, and
// beside a reader that has begun to fill it takes the reads.
func TestAReaderWithNoMachineRowIsUnbounded(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 2, 24, 40)
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-x"))
	w.s.ReaderStates["reader-x"] = ReaderUp
	w.part(TickAsk, TickReq{})
	assert.Equal(t, 40, w.s.readerLoad("reader-m1")+w.s.readerLoad("reader-m2")+w.s.readerLoad("reader-x"), "every read is asked")
	assert.LessOrEqual(t, w.s.readerLoad("reader-m1"), 2)
	assert.LessOrEqual(t, w.s.readerLoad("reader-m2"), 24)
	assert.Positive(t, w.s.readerLoad("reader-x"))
}

// The level moves asked reads off a reader over its width to one with room,
// and gives a reader at width nothing: reads asked when m1 was wide, with its
// width then lowered, move to m2 until m1 is at its width or m2 is at its.
func TestTheLevelMovesReadsOffAReaderOverItsWidth(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 8, 4, 8)
	w.part(TickAsk, TickReq{})
	require.Equal(t, 8, w.s.readerLoad("reader-m1")+w.s.readerLoad("reader-m2"))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
	w.part(TickLevelReads, TickReq{})
	assert.Equal(t, 4, w.s.readerLoad("reader-m2"), "m2 is filled to its width and no further")
	assert.Equal(t, 4, w.s.readerLoad("reader-m1"), "the rest stay: no reader has room")
	for _, c := range w.s.Readers.Column(Asked) {
		if c.F(FieldLeveled) != "" {
			assert.Equal(t, "reader-m2", c.Row)
		}
	}
}

// A pro card's reads are asked together, each of a different reader with room
// (ReadsWanted, askPicks): with all the free room on one reader (m1 width 2
// and idle, m2 width 1 and full) its second read, which needs a different
// reader, waits for m2's room, and the first with it, due, with no judgment;
// the ask's refusal (NCannotAsk) is for a card no readers could ever read,
// never for want of room. When m2's lane frees, both reads are asked, of m1
// and m2.
func TestAProCardsSecondReadWaitsForAnotherReaderWithRoom(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 2, 1, 2)
	w.s.Work.Card("p2").Fields["brief"] = proBrief
	w.s.Readers.Put(&Card{ID: ReadCardID("p1", 1, "reader-m2"), Row: "reader-m2", Col: Asked, Rev: 1,
		Fields: map[string]string{"kind": "read", "primary": "p1", "stream": "s1", "attempt": "1", "reader": "reader-m2", "head": "h1"}})
	p, due := TickAsk(w.s, TickReq{})
	assert.Empty(t, p.Units, "the second read needs another reader, and m2 is at width")
	assert.Empty(t, p.Notes, "waiting for a second reader with room is no judgment")
	assert.Equal(t, 1, due, "the pro card is due")
	assert.Empty(t, readsAt(w.s, w.s.Work.Card("p2"), 1), "neither read is asked alone")
	w.s.Readers.Card(ReadCardID("p1", 1, "reader-m2")).Col = OK
	w.s.Readers.cells = nil
	w.part(TickAsk, TickReq{})
	reads := readsAt(w.s, w.s.Work.Card("p2"), 1)
	require.Len(t, reads, 2, "m2's lane freed: both reads asked together")
	assert.ElementsMatch(t, []string{"reader-m1", "reader-m2"}, []string{reads[0].Row, reads[1].Row})
}

// A read its reader returned is asked again in place when no other reader has
// room: the room it takes is the room its reader already holds for it, so the
// tick asks it and waits for nothing.
func TestAReturnedReadIsAskedAgainInPlaceWithNoRoomElsewhere(t *testing.T) {
	t.Parallel()
	w := widthWorld(t, 1, 1, 2)
	w.part(TickAsk, TickReq{})
	require.Equal(t, 2, w.s.readerLoad("reader-m1")+w.s.readerLoad("reader-m2"), "both readers at width")
	rc := readsAt(w.s, w.s.Work.Card("p1"), 1)[0]
	rc.Fields[FieldReturned] = stamp(w.s.Now)
	p, due := TickAsk(w.s, TickReq{})
	w.must(p)
	assert.Equal(t, 0, due, "the returned read is asked again in place: nothing waits")
	assert.Empty(t, p.Notes)
	rc = readsAt(w.s, w.s.Work.Card("p1"), 1)[0]
	assert.Empty(t, rc.F(FieldReturned), "asked again")
	assert.Equal(t, 1, w.s.readerLoad(rc.Row), "its reader holds it once")
}
