package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestAReviewCardIsAskedAReadTheSameTick(t *testing.T) {
	t.Parallel()

	t.Run("a free reader is asked in the tick the card reaches review", func(t *testing.T) {
		t.Parallel()
		w := widthWorld(t, 2, 2, 1)
		tickAsk(t, w, nil)
		require.Len(t, liveReadsAt(w.s, w.s.Work.Card("p1"), 1), 1, "asked in the same tick")
		assert.Empty(t, w.notesOf(NWaitingForReader), "asked: nothing waits")
		assert.Empty(t, w.s.Work.Card("p1").F(FieldWaitingReader))
		assert.Empty(t, stalls(w, "p1"))
	})

	t.Run("no reader with room: the note, then the ask when one frees", func(t *testing.T) {
		t.Parallel()
		w := widthWorld(t, 1, 1, 3)
		_, due := tickAsk(t, w, nil)
		require.Equal(t, 1, due, "the third read waits for room")
		ns := w.notesOf(NWaitingForReader)
		require.Len(t, ns, 1, "the card waiting is told: %+v", w.notes)
		assert.Equal(t, Happened, ns[0].Kind, "waiting for room is no judgment")
		assert.Equal(t, []string{"p3"}, ns[0].Primaries)
		assert.NotEmpty(t, w.s.Work.Card("p3").F(FieldWaitingReader), "recorded on the card")
		assert.Empty(t, stalls(w, "p3"), "waiting for a reader is held, not stalled")
		assert.Empty(t, w.notesOf(NStalled))
		assert.Empty(t, w.notesOf(NStranded))

		// the next tick, with no room still: no second note
		_, due = tickAsk(t, w, nil)
		assert.Equal(t, 1, due)
		assert.Len(t, w.notesOf(NWaitingForReader), 1, "once an attempt")
		assert.Empty(t, stalls(w, "p3"))

		// a reader frees: the tick asks it at once, and the note is cleared
		var freed *Card
		for _, c := range w.s.Readers.Column(Asked) {
			freed = c
			break
		}
		require.NotNil(t, freed)
		freed.Col = OK
		w.s.Readers.cells = nil
		_, due = tickAsk(t, w, nil)
		assert.Zero(t, due)
		require.Len(t, liveReadsAt(w.s, w.s.Work.Card("p3"), 1), 1, "asked the moment a reader frees")
		assert.Empty(t, w.s.Work.Card("p3").F(FieldWaitingReader), "the note's mark is cleared by the ask")
		assert.Empty(t, stalls(w, "p3"))
	})

	t.Run("a frontier read with a friend free is asked of her in the same tick", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", "s1-1: work (s1) tier: frontier\n", 1, 1, "head")
		seats := []FriendSeat{frontierSeat("amy", 1, Up, "")}
		tickAsk(t, w, seats)
		require.NotNil(t, w.s.Fleet.Placed(ReadCardID("s1-1", 1, "amy")))
		assert.Empty(t, w.notesOf(NWaitingForReader))
	})

	t.Run("a frontier read with every friend at her room waits with the note, no judgment", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		for i, id := range []string{"s1-1", "s1-2", "s1-3"} {
			putReview(w, id, id+": work (s1) tier: frontier\n", 1, float64(i+1), "head")
		}
		seats := []FriendSeat{frontierSeat("amy", 1, Up, "")}
		w.s.Friends = seats
		_, due := tickAsk(t, w, seats)
		require.Equal(t, 1, due, "the third waits for her room")
		ns := w.notesOf(NWaitingForReader)
		require.Len(t, ns, 1, "%+v", w.notes)
		assert.Equal(t, []string{"s1-3"}, ns[0].Primaries)
		assert.Empty(t, w.notesOf(NFewReaders), "a friend up at her room is no judgment")
		assert.Empty(t, stalls(w, "s1-3"), "waiting for a friend is held, not stalled")

		// her first read comes back: the third is asked of her at once
		w.must(FriendReadClose(w.s, "amy", "s1-1", "Verdict: LAND\n"))
		_, due = tickAsk(t, w, seats)
		assert.Zero(t, due)
		require.NotNil(t, w.s.Fleet.Placed(ReadCardID("s1-3", 1, "amy")))
		assert.Empty(t, w.s.Work.Card("s1-3").F(FieldWaitingReader))
	})

	t.Run("a frontier read taken back from a friend is asked again", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", "s1-1: work (s1) tier: frontier\n", 1, 1, "head")
		seats := []FriendSeat{frontierSeat("amy", 1, Up, ""), frontierSeat("bob", 1, Up, "")}
		tickAsk(t, w, seats)
		var first *Card
		for _, n := range []string{"amy", "bob"} {
			if c := w.s.Fleet.Placed(ReadCardID("s1-1", 1, n)); c != nil {
				first = c
			}
		}
		require.NotNil(t, first)
		w.must(Plan{Units: []Unit{{Key: "s1-1", Changes: []Change{change(Fleet, removeEntry(first, map[string]string{"retired": stamp(w.s.Now), "retired_by": "taken back"}))}}}})
		tickAsk(t, w, seats)
		other := "amy"
		if first.F("reader") == "amy" {
			other = "bob"
		}
		assert.NotNil(t, w.s.Fleet.Placed(ReadCardID("s1-1", 1, other)), "a read taken back is not a read: asked of the other friend")
	})

	t.Run("a frontier read with no friend up is the readers' judgment, which holds it", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", "s1-1: work (s1) tier: frontier\n", 1, 1, "head")
		seats := []FriendSeat{frontierSeat("amy", 1, Down, "")}
		w.s.Friends = seats
		tickAsk(t, w, seats)
		require.Len(t, w.notesOf(NFewReaders), 1)
		assert.Empty(t, stalls(w, "s1-1"), "held by the judgment, not stalled")
		// the next tick keeps it open: the machine's ask, with its readers up, does
		// not close the friends' judgment
		p, _ := tickAsk(t, w, seats)
		for _, o := range p.Closes {
			assert.NotEqual(t, NFewReaders, o.Note.Type, "closed while no friend is up")
		}
		assert.Len(t, w.notesOf(NFewReaders), 1, "not written again")
		assert.Empty(t, stalls(w, "s1-1"))
	})
}
