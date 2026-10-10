package sprint

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestAFrontierCardsReadIsAskedAsAFriendCard pins the tick: a frontier card
// in review, and a heavy card whose read tier is frontier, is asked of a
// frontier friend. A friend at or above the read tier with room is asked
// before a paid reader. The ask writes her inbox brief and places no
// readers-table card while she has room. The world clock is t0; the deadline
// is thirty minutes on it.
func TestAFrontierCardsReadIsAskedAsAFriendCard(t *testing.T) {
	t.Parallel()
	const (
		branch   = "sprint/frontier-work"
		start    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		workHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		primHead = "primary-head"
	)
	body := "s1-1: read this (s1) tier: frontier\n\nAS A READ\nthe body line\n\na blank stays above this\nRULES\nnot in the brief\n"

	t.Run("tick", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		dir := t.TempDir()
		putReview(w, "s1-1", body, 2, 1, primHead)
		putAttemptWork(w, "s1-1", 1, "sprint/old", start, "yes")
		putAttemptWork(w, "s1-1", 2, branch, workHead, "")
		require.Equal(t, cardhdr.RoutePro, w.s.readTierOf(w.s.Work.Card("s1-1"))) // the collapse: frontier is read on heavy, the strongest tier a route serves, and heavy on pro (the interim rule); the friend is asked the tier before it
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, dir)})

		id := ReadCardID("s1-1", 2, "amy")
		rc := w.s.Fleet.Card(id)
		require.NotNil(t, rc)
		require.Equal(t, FriendRow("amy"), rc.Row)
		require.Equal(t, Working, rc.Col)
		require.Equal(t, "amy", rc.F("reader"))
		require.Equal(t, branch, rc.F("branch"))
		require.Equal(t, start, rc.F("start"))
		require.Equal(t, workHead, rc.F("head"))
		require.Equal(t, []string{"reader-a", "reader-b"}, w.s.Readers.Rows())
		require.Nil(t, w.s.Readers.Card(id))
		require.Equal(t, Review, w.s.Work.Card("s1-1").Col)

		text := string(mustRead(t, filepath.Join(dir, "inbox", id, "BRIEF.md")))
		require.Contains(t, text, "WHO: friend amy\n")
		require.Contains(t, text, "branch: "+branch+"\n")
		require.Contains(t, text, "start: "+start+"\n")
		require.Contains(t, text, "head: "+workHead+"\n")
		require.NotContains(t, text, "start: "+workHead)
		deadline := t0.Add(FriendReadDeadline).UTC().Format(time.RFC3339)
		require.Equal(t, "2030-01-02T03:34:05Z", deadline)
		require.Contains(t, text, "deadline: "+deadline+"\n")
		require.Contains(t, text, "AS A READ\nthe body line\n\na blank stays above this\n")
		require.NotContains(t, text, "RULES")
		require.NotContains(t, text, primHead)
	})

	t.Run("pro beside frontier", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", body, 1, 1, primHead)
		putReview(w, "s1-2", "s1-2: other (s1) tier: pro\n", 1, 2, "pro-head")
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, t.TempDir())})
		require.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "amy")))
		require.NotNil(t, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")))
		// pro is below frontier: she takes that read too, and no paid reader is asked while she has room
		require.NotNil(t, w.s.Fleet.Card(ReadCardID("s1-2", 1, "amy")))
		require.Nil(t, w.s.Readers.Card(ReadCardID("s1-2", 1, "reader-a")))
		require.Nil(t, w.s.Readers.Card(ReadCardID("s1-2", 1, "reader-b")))
		for _, c := range w.s.Readers.Cards() {
			require.False(t, c.Placed())
		}
	})

	t.Run("heavy read tier frontier", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-h", "s1-h: heavy (s1) tier: heavy\n\nAS A READ\nheavy body\n", 1, 1, primHead)
		w.s.Work.SetProp(PropReadTier, cardhdr.RouteFrontier)
		require.Equal(t, "pro", w.s.readTierOf(w.s.Work.Card("s1-h"))) // heavy is read on pro (the interim rule); the friend is asked the tier before it
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, t.TempDir())})
		id := ReadCardID("s1-h", 1, "amy")
		require.NotNil(t, w.s.Fleet.Card(id))
		require.Equal(t, Working, w.s.Fleet.Card(id).Col)
		require.Nil(t, w.s.Readers.Card(id))
		for _, c := range w.s.Readers.Cards() {
			require.False(t, c.Placed())
		}
	})

	t.Run("width", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		for i, id := range []string{"s1-1", "s1-2", "s1-3"} {
			putReview(w, id, id+": work (s1) tier: frontier\n", 1, float64(i+1), primHead)
		}
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 1, Up, t.TempDir())})
		require.Equal(t, Working, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")).Col)
		require.Equal(t, Ready, w.s.Fleet.Card(ReadCardID("s1-2", 1, "amy")).Col)
		require.Nil(t, w.s.Fleet.Card(ReadCardID("s1-3", 1, "amy")))
		// she is at her room and a paid reader has room: the third is asked of a reader
		require.NotNil(t, placedReaderRead(w, "s1-3"))
		require.Empty(t, w.notesOf(NFewReaders))
		require.Empty(t, w.notesOf(NWaitingForReader))
	})

	t.Run("one-shot mode", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		for i, id := range []string{"s1-1", "s1-2", "s1-3"} {
			putReview(w, id, id+": work (s1) tier: frontier\n", 1, float64(i+1), primHead)
		}
		seat := frontierSeat("amy", 2, Up, t.TempDir())
		seat.Mode = config.FriendModeOneShot
		askReaders(t, w, []FriendSeat{seat})
		// a one-shot friend reads to her width as a batch friend does (one lane per unit of
		// width, the owner 2026-10-10): two lanes working and the third ready behind them
		require.Equal(t, Working, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")).Col)
		require.Equal(t, Working, w.s.Fleet.Card(ReadCardID("s1-2", 1, "amy")).Col)
		require.Equal(t, Ready, w.s.Fleet.Card(ReadCardID("s1-3", 1, "amy")).Col)
		require.Empty(t, w.notesOf(NWaitingForReader))
	})

	t.Run("most free", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", "s1-1: work (s1) tier: frontier\n", 1, 1, primHead)
		putReview(w, "s1-2", "s1-2: work (s1) tier: frontier\n", 1, 2, primHead)
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 1, Up, ""), frontierSeat("bob", 1, Up, "")})
		require.Equal(t, FriendRow("amy"), w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")).Row)
		require.Equal(t, FriendRow("bob"), w.s.Fleet.Card(ReadCardID("s1-2", 1, "bob")).Row)
	})

	t.Run("none up", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		dir := t.TempDir()
		putReview(w, "s1-1", "s1-1: work (s1) tier: frontier\n", 1, 1, primHead)
		putReview(w, "s1-2", "s1-2: work (s1) tier: frontier\n", 1, 2, primHead)
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Down, dir)})
		require.Empty(t, w.notesOf(NFewReaders), "readers are up: a down friend is not fewer readers")
		require.NoFileExists(t, filepath.Join(dir, "inbox"))
		require.Nil(t, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")))
		require.NotNil(t, placedReaderRead(w, "s1-1"))
		require.NotNil(t, placedReaderRead(w, "s1-2"))
	})

	t.Run("below the tier", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", "s1-1: work (s1) tier: pro\n", 1, 1, primHead)
		askReaders(t, w, []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteFlash}, Dir: t.TempDir()}})
		require.Nil(t, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")), "a flash friend is below a pro read")
		require.NotNil(t, placedReaderRead(w, "s1-1"))
	})

	t.Run("land", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", body, 2, 1, primHead)
		putAttemptWork(w, "s1-1", 1, "sprint/old", start, "yes")
		putAttemptWork(w, "s1-1", 2, branch, workHead, "")
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, t.TempDir())})
		id := ReadCardID("s1-1", 2, "amy")
		w.must(FriendReadCloseChecked(w.s, "amy", "s1-1", "", "Verdict: LAND\n", nil))
		rc := w.s.Fleet.Card(id)
		require.NotNil(t, rc)
		require.False(t, rc.Placed())
		require.Equal(t, "ok", rc.F("verdict"))
		require.NotEqual(t, Broken, rc.Col)
		require.Equal(t, []string{"reader-a", "reader-b"}, w.s.Readers.Rows())
		require.Nil(t, w.s.Readers.Card(id))
		require.Equal(t, Review, w.s.Work.Card("s1-1").Col)
		require.Empty(t, w.notesOf(NReadBroken))
	})

	t.Run("stale outbox generation", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", body, 2, 1, primHead)
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, t.TempDir())})
		rc := w.s.Fleet.Card(ReadCardID("s1-1", 2, "amy"))
		require.NotNil(t, rc)
		rc.Fields["gen"] = "2" // same read returned and taken after a STOP
		late := FriendReadCloseChecked(w.s, "amy", "s1-1", "", "Verdict: LAND\n", nil, 1)
		require.NotEmpty(t, late.Refused, "the old outbox cannot close a resumed read")
		require.True(t, rc.Placed())
		w.must(FriendReadCloseChecked(w.s, "amy", "s1-1", "", "Verdict: LAND\n", nil, 2))
	})

	t.Run("hold", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		putReview(w, "s1-1", body, 2, 1, primHead)
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, t.TempDir())})
		id := ReadCardID("s1-1", 2, "amy")
		w.must(FriendReadCloseChecked(w.s, "amy", "s1-1", "", "Verdict: HOLD\nmain.go is wrong\n", nil))
		rc := w.s.Fleet.Card(id)
		require.False(t, rc.Placed())
		require.Equal(t, "broken", rc.F("verdict"))
		require.Contains(t, rc.F("finding"), "main.go")
		require.NotEqual(t, Broken, rc.Col)
		require.Nil(t, w.s.Readers.Card(id))
		ns := w.notesOf(NReadBroken)
		require.Len(t, ns, 1)
		require.Contains(t, ns[0].What, "main.go")
		require.Equal(t, "amy", ns[0].Who)
	})
}

func putReview(w *world, id, brief string, attempt int, score float64, head string) {
	w.s.Work.SetRows([]string{"s1"})
	w.s.Work.Put(&Card{ID: id, Row: "s1", Col: Review, Score: score, Rev: 1, Fields: map[string]string{
		"kind": "primary", "attempt": itoa(attempt), "stream": "s1", "brief": brief, "head": head}})
}

func putAttemptWork(w *world, primary string, attempt int, branch, head, ok string) {
	fields := map[string]string{"kind": "work", "primary": primary, "stream": "s1", "attempt": itoa(attempt), "branch": branch, "head": head}
	if ok != "" {
		fields["ok"] = ok
	}
	w.s.Fleet.Put(&Card{ID: WorkCardID(primary, attempt), Rev: 1, Fields: fields})
}

func placedReaderRead(w *world, primary string) *Card {
	for _, c := range w.s.Readers.Cards() {
		if c.Placed() && c.F("kind") == "read" && c.F("primary") == primary {
			return c
		}
	}
	return nil
}

func frontierSeat(name string, width int, status, dir string) FriendSeat {
	return FriendSeat{Name: name, Width: width, Status: status, Tiers: []string{cardhdr.RouteFrontier}, Dir: dir}
}

func askReaders(t *testing.T, w *world, seats []FriendSeat) {
	t.Helper()
	var fn TickPartFn
	for _, u := range TickTables {
		if u.Table != Readers {
			continue
		}
		for _, part := range u.Parts {
			if part.Name == "ask" {
				fn = part.Fn
			}
		}
	}
	require.NotNil(t, fn, "the readers ask part is not installed")
	w.part(fn, TickReq{Friends: seats})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}
