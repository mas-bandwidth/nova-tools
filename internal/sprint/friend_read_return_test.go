package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// friendReadAsked is a world with s1-1 in review at attempt 1 and its read asked of
// friend amy on her fleet row (FriendReadAsk), and the read card's id.
func friendReadAsked(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	putReview(w, "s1-1", "s1-1: work (s1) tier: frontier\n\nAS A READ\nread it\n", 1, 1, "head-1")
	askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, "")})
	id := ReadCardID("s1-1", 1, "amy")
	require.NotNil(t, w.s.Fleet.Placed(id))
	return w, id
}

// A read asked of a friend row is written at a live generation, from 1, as her
// work cards are: queue, finish and her QUEUE.json name it at the one it holds
// (docs/SPEC-SPRINT.md section 5).
func TestAFriendRowReadIsWrittenWithALiveGeneration(t *testing.T) {
	t.Parallel()
	w, id := friendReadAsked(t)
	rc := w.s.Fleet.Card(id)
	require.Equal(t, "read", rc.F("kind"))
	require.Equal(t, 1, rc.Int("gen"), "a read on her row is at generation 1, never 0")
}

// read --as friend.<f> (--ok | --broken) <read> closes a read on her fleet row as her
// outbox report does (FriendReadClose): the verdict stored, the card retired off her row,
// a broken verdict's judgment raised, the readers table untouched. A read asked before
// the ask wrote a generation (gen 0, today's shape) is closed the same way.
func TestReadAsFriendClosesAFriendRowRead(t *testing.T) {
	t.Parallel()
	for _, gen0 := range []bool{false, true} {
		t.Run(map[bool]string{false: "gen 1", true: "gen 0"}[gen0], func(t *testing.T) {
			t.Parallel()
			t.Run("ok", func(t *testing.T) {
				t.Parallel()
				w, id := friendReadAsked(t)
				if gen0 {
					delete(w.s.Fleet.Card(id).Fields, "gen")
				}
				w.must(Read(w.s, ReadReq{Sel: Sel{IDs: []string{id}}, As: FriendRow("amy"), Verdict: "ok", Who: FriendRow("amy")}))
				rc := w.s.Fleet.Card(id)
				require.False(t, rc.Placed(), "the read is retired off her row")
				require.Equal(t, "ok", rc.F("verdict"))
				require.Equal(t, "read", rc.F("retired_by"))
				require.Nil(t, w.s.Readers.Card(id))
				require.Equal(t, []string{"reader-a", "reader-b"}, w.s.Readers.Rows(), "the readers table gains no friend row")
				require.Empty(t, w.notesOf(NReadBroken))
			})
			t.Run("broken", func(t *testing.T) {
				t.Parallel()
				w, id := friendReadAsked(t)
				if gen0 {
					delete(w.s.Fleet.Card(id).Fields, "gen")
				}
				w.must(Read(w.s, ReadReq{Sel: Sel{IDs: []string{id}}, As: FriendRow("amy"), Verdict: "broken", Finding: "main.go:12 drops the error; return it", Who: FriendRow("amy")}))
				rc := w.s.Fleet.Card(id)
				require.False(t, rc.Placed())
				require.Equal(t, "broken", rc.F("verdict"))
				require.Contains(t, rc.F("finding"), "main.go:12")
				ns := w.notesOf(NReadBroken)
				require.Len(t, ns, 1)
				require.Equal(t, "amy", ns[0].Who)
			})
		})
	}
	t.Run("the same close as her outbox report", func(t *testing.T) {
		t.Parallel()
		a, id := friendReadAsked(t)
		b, _ := friendReadAsked(t)
		va := Read(a.s, ReadReq{Sel: Sel{IDs: []string{id}}, As: FriendRow("amy"), Verdict: "ok"})
		vb := FriendReadClose(b.s, "amy", "s1-1", "Verdict: LAND\n")
		require.Len(t, va.Units, 1)
		require.Len(t, vb.Units, 1)
		require.Equal(t, vb.Units[0].Changes, va.Units[0].Changes)
		require.Equal(t, vb.Units[0].Notes, va.Units[0].Notes)
	})
	t.Run("by selection", func(t *testing.T) {
		t.Parallel()
		w, id := friendReadAsked(t)
		w.must(Read(w.s, ReadReq{As: FriendRow("amy"), Verdict: "ok"}))
		require.Equal(t, "ok", w.s.Fleet.Card(id).F("verdict"))
	})
	t.Run("no begin, no return", func(t *testing.T) {
		t.Parallel()
		w, id := friendReadAsked(t)
		p := Read(w.s, ReadReq{Sel: Sel{IDs: []string{id}}, As: FriendRow("amy"), Begin: true})
		require.Empty(t, p.Units)
		require.NotEmpty(t, p.Refused)
		require.Contains(t, p.Refused[0].Why, "outbox/<read>/REPORT.md")
	})
	t.Run("another friend's read", func(t *testing.T) {
		t.Parallel()
		w, id := friendReadAsked(t)
		p := Read(w.s, ReadReq{Sel: Sel{IDs: []string{id}}, As: FriendRow("bob"), Verdict: "ok"})
		require.Empty(t, p.Units)
		require.NotEmpty(t, p.Refused)
		require.True(t, w.s.Fleet.Card(id).Placed())
	})
}

// finish on a read on a friend's row is refused with the remedy that returns it: her
// outbox report, or the read verb; never the generation's refusal, at gen 1 or gen 0.
func TestFinishRefusesAReadWithTheOutboxRemedy(t *testing.T) {
	t.Parallel()
	for _, gen0 := range []bool{false, true} {
		w, id := friendReadAsked(t)
		if gen0 {
			delete(w.s.Fleet.Card(id).Fields, "gen")
		}
		p := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{id}}, As: FriendRow("amy"), Gens: map[string]int{id: 1}, Report: "done"})
		require.Empty(t, p.Units)
		require.Len(t, p.Refused, 1)
		why := p.Refused[0].Why
		require.Contains(t, why, "a read, not work")
		require.Contains(t, why, "outbox/"+id+"/REPORT.md")
		require.Contains(t, why, "read --as friend.amy (--ok | --broken) "+id)
		require.NotContains(t, why, "generation")
	}
}
