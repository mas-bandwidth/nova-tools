package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// cannotAskEpisodeWorld is a read-cards world with one heavy primary in review whose attempt
// bob worked: with bob the only heavy reader no unit up may read it (NoReaderMayRead); with
// amy beside him at no room every reader who may read it is at its room; with amy at width
// 8 and cal beside them it is dealt its two reads.
func cannotAskEpisodeWorld(t *testing.T) (w *world, none, room, free []FriendSeat) {
	t.Helper()
	w = readCardsWorld(t, 4)
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: heavy\n", "bob", 1)
	bob := readerSeat("bob", 8, []string{"heavy"}, []string{"builder", "reader"})
	none = []FriendSeat{bob}
	room = []FriendSeat{bob, readerSeat("amy", 0, []string{"heavy"}, []string{"builder", "reader"})}
	free = []FriendSeat{bob, readerSeat("amy", 8, []string{"heavy"}, []string{"builder", "reader"}),
		readerSeat("cal", 8, []string{"heavy"}, []string{"builder", "reader"})} // heavy wants two reads
	return w, none, room, free
}

// askTick runs the read-card ask part on the world as the tick would, the clock a minute on,
// and is the cannot-ask judgments it writes and the ones it closes.
func askTick(t *testing.T, w *world, seats []FriendSeat) (raised []Note, closed []Open) {
	t.Helper()
	w.s.Now = w.s.Now.Add(time.Minute)
	p := w.part(func(s *Snapshot, r TickReq) (Plan, int) { return readCardsAskPart(s, r, seats) }, TickReq{Friends: seats})
	for _, n := range p.Notes {
		if n.Type == NCannotAsk {
			raised = append(raised, n)
		}
	}
	for _, o := range p.Closes {
		if o.Note.Type == NCannotAsk {
			closed = append(closed, o)
		}
	}
	return raised, closed
}

// openCannotAsk is the cannot-ask judgments open on the primary.
func openCannotAsk(w *world, id string) []Open {
	return closesFor(w.s.Open, []string{NCannotAsk}, id)
}

// waitOf is why the read-card ask leaves the primary waiting with the seats.
func waitOf(w *world, seats []FriendSeat, id string) string {
	_, waits := readCardsAsk(w.s, seats, nil)
	return waits[id]
}

// TestCannotAskIsOneJudgmentAnEpisode pins fault 22 of the 2026-10-10 inventory: a primary
// in review waiting for a reader at one attempt is one cannot-ask judgment, whatever the
// wait's reason each tick. Its reason turning from "no reader up may read it" to "every
// reader up who may read it is at its room" and back neither closes the judgment nor
// raises a second (live: fix-help-decide-hb-b1-b2b, six judgments at attempt 2); nor does it
// close the coordinator's wait on it, to raise it again before the wait runs out.
func TestCannotAskIsOneJudgmentAnEpisode(t *testing.T) {
	t.Parallel()
	t.Run("the wait's reason turns", func(t *testing.T) {
		t.Parallel()
		w, none, room, _ := cannotAskEpisodeWorld(t)
		require.True(t, strings.HasPrefix(waitOf(w, none, "s1-1"), NoReaderMayRead), waitOf(w, none, "s1-1"))
		require.Equal(t, "every reader up who may read it is at its room", waitOf(w, room, "s1-1"))

		raised, _ := askTick(t, w, none)
		require.Len(t, raised, 1, "the episode is raised")
		first := openCannotAsk(w, "s1-1")
		require.Len(t, first, 1)

		for i, seats := range [][]FriendSeat{room, none, room, none} {
			raised, closed := askTick(t, w, seats)
			require.Empty(t, closed, "tick %d: the primary still waits at attempt 1: its judgment stays open", i)
			require.Empty(t, raised, "tick %d: one judgment an episode, never a second", i)
			require.Equal(t, first, openCannotAsk(w, "s1-1"), "tick %d: the same judgment", i)
		}
	})
	t.Run("the coordinator's wait holds through the turn", func(t *testing.T) {
		t.Parallel()
		w, none, room, _ := cannotAskEpisodeWorld(t)
		askTick(t, w, none)
		j := openCannotAsk(w, "s1-1")
		require.Len(t, j, 1)
		// the coordinator waits it for 30m (Wait): the judgment closed, a hold on its condition
		w.closeAll(j)
		hold := Open{Key: OpenKey("hold-1", "s1-1"), Note: Note{ID: "hold-1", Kind: Acknowledged, Type: NCannotAsk, Stream: "s1",
			Primaries: []string{"s1-1"}, Count: 1, At: w.s.Now, Review: w.s.Now.Add(30 * time.Minute), ReviewSet: w.s.Now}}
		w.s.Acked = append(w.s.Acked, hold)

		for i, seats := range [][]FriendSeat{room, none} {
			raised, closed := askTick(t, w, seats)
			require.Empty(t, closed, "tick %d: the wait stands while the primary waits at its attempt", i)
			require.Empty(t, raised, "tick %d: not raised again before the wait runs out", i)
		}
	})
}

// TestCannotAskClosesWhenItsEpisodeEnds pins the other half: the judgment closes when its
// cause clears: the primary is dealt its read (it waits no more), leaves review, or its
// attempt moves on (a new episode, raised on a later tick and not in the tick that ends the
// one before).
func TestCannotAskClosesWhenItsEpisodeEnds(t *testing.T) {
	t.Parallel()
	raise := func(t *testing.T) (*world, []FriendSeat, []FriendSeat) {
		w, none, _, free := cannotAskEpisodeWorld(t)
		raised, _ := askTick(t, w, none)
		require.Len(t, raised, 1)
		require.Len(t, openCannotAsk(w, "s1-1"), 1)
		return w, none, free
	}
	t.Run("a reader may read it", func(t *testing.T) {
		t.Parallel()
		w, _, free := raise(t)
		require.Empty(t, waitOf(w, free, "s1-1"), "amy and cal with room are dealt its reads")
		raised, closed := askTick(t, w, free)
		require.Len(t, closed, 1, "dealt its read: the episode ends")
		require.Empty(t, raised)
		require.Empty(t, openCannotAsk(w, "s1-1"))
		require.Empty(t, w.s.Work.Card("s1-1").F(FieldWaitingReader), "it waits no more")
	})
	t.Run("it leaves review", func(t *testing.T) {
		t.Parallel()
		w, none, _ := raise(t)
		w.place(w.s.Work, "s1-1", "s1", Ready)
		raised, closed := askTick(t, w, none)
		require.Len(t, closed, 1, "out of review: the episode ends")
		require.Empty(t, raised)
		require.Empty(t, openCannotAsk(w, "s1-1"))
	})
	t.Run("its attempt moves on", func(t *testing.T) {
		t.Parallel()
		w, none, _ := raise(t)
		require.Equal(t, 1, waitingAt(w.s.Work.Card("s1-1")), "marked waiting at attempt 1")
		// reworked and back in review at attempt 2, bob its worker again, in one tick
		w.s.Work.Card("s1-1").Fields["attempt"] = "2"
		putAttemptWork(w, "s1-1", 2, "sprint/s1-1", "work2-s1-1", "yes")
		w.s.Fleet.Card(WorkCardID("s1-1", 2)).Fields["member"] = "bob"
		require.True(t, strings.HasPrefix(waitOf(w, none, "s1-1"), NoReaderMayRead))

		raised, closed := askTick(t, w, none)
		require.Len(t, closed, 1, "the judgment of attempt 1 ends with its attempt")
		require.Empty(t, raised, "the next episode is raised once the last is closed")
		require.Empty(t, openCannotAsk(w, "s1-1"))
		require.Equal(t, 2, waitingAt(w.s.Work.Card("s1-1")), "marked waiting at attempt 2")

		raised, closed = askTick(t, w, none)
		require.Empty(t, closed)
		require.Len(t, raised, 1, "attempt 2's episode, one judgment")
		raised, _ = askTick(t, w, none)
		require.Empty(t, raised)
	})
}
