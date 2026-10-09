package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// missingReport is a friend's broken report on a read whose branch origin does not hold.
const missingReport = "Verdict: HOLD\nRULE: the branch this read names is not on origin; there is no commit to judge\n"

// friendReadWorld is a world with read cards on, s1-1 (flash) in review worked by m1, and its
// one read dealt to friend amy (m1 worked it, so only she may read it), and her read card.
func friendReadWorld(t *testing.T) (*world, []FriendSeat, *Card) {
	t.Helper()
	w := readCardsWorld(t, 4, "m1")
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "m1", 1)
	seats := []FriendSeat{readerSeat("amy", 2, []string{"flash"}, []string{"reader"})}
	dealReads(t, w, seats)
	reads := readCardsOf(w, "s1-1")
	require.Len(t, reads, 1, "the fixture: one read, amy's")
	require.Equal(t, FriendRow("amy"), reads[0].Row)
	require.Equal(t, ReadCardID("s1-1", 1, "amy"), reads[0].ID)
	return w, seats, reads[0]
}

// A friend's read asked again after a read on a missing branch is her read card's second
// identity (ReadCardGenIDs), and friend sync closes it by its own key, the packet's card
// (docs/SPEC-SPRINT.md section 6, a read on a branch origin does not hold).
func TestAReaskedFriendReadClosesByItsOwnKey(t *testing.T) {
	t.Parallel()
	w, seats, first := friendReadWorld(t)
	named, holds := "sprint/s1-1.w1.g3.e0", "sprint/s1-1.w1.g1.e0"
	w.must(FriendReadCloseChecked(w.s, "amy", "s1-1", first.ID, missingReport,
		map[string]MissingBranch{first.ID: {Named: named, Holds: holds}}))
	require.Equal(t, RetiredByMissingBranch, w.s.Fleet.Card(first.ID).F("retired_by"))
	require.Empty(t, w.notesOf(NReadBroken), "no verdict, no rework")
	require.Empty(t, w.notesOf(NReadBranchMissing), "a branch holds the head: asked again, no judgment")

	dealReads(t, w, seats)
	again := readCardsOf(w, "s1-1")
	require.Len(t, again, 1, "her read is asked again")
	require.Equal(t, ReadCardID("s1-1", 1, "amy")+".g1", again[0].ID, "under her second identity")
	require.Equal(t, FriendRow("amy"), again[0].Row)

	// the retired first identity is not hers to close
	p := FriendReadCloseChecked(w.s, "amy", "s1-1", first.ID, "Verdict: LAND\n", nil)
	require.NotEmpty(t, p.Refused)
	// her report on the read asked again closes it, by its own key
	w.must(FriendReadCloseChecked(w.s, "amy", "s1-1", again[0].ID, "Verdict: LAND\n", nil))
	rc := w.s.Fleet.Card(again[0].ID)
	require.False(t, rc.Placed(), "the read asked again is closed")
	require.Equal(t, "ok", rc.F("verdict"))
	// and with no key named, the placed one of her identities is the one closed
	w2, seats2, first2 := friendReadWorld(t)
	w2.must(FriendReadCloseChecked(w2.s, "amy", "s1-1", first2.ID, missingReport,
		map[string]MissingBranch{first2.ID: {Named: named, Holds: holds}}))
	dealReads(t, w2, seats2)
	w2.must(FriendReadClose(w2.s, "amy", "s1-1", "Verdict: LAND\n"))
	require.Equal(t, "ok", w2.s.Fleet.Card(first2.ID+".g1").F("verdict"))
}

// When no branch of the work on origin holds the read's head, the seat is told at once, one
// judgment on the primary naming the read card and the branch, and the read is not asked
// again while it is open: a read asked again could only find the same. The seat's ack (the
// branch pushed) lets the tick ask it again.
func TestNoBranchHoldingTheHeadIsJudgedAtOnceAndNotReasked(t *testing.T) {
	t.Parallel()
	w, seats, first := friendReadWorld(t)
	named := "sprint/s1-1.w1.g3.e0"
	w.must(FriendReadCloseChecked(w.s, "amy", "s1-1", first.ID, missingReport,
		map[string]MissingBranch{first.ID: {Named: named}}))
	js := w.notesOf(NReadBranchMissing)
	require.Len(t, js, 1, "one judgment, at the first missing branch")
	require.Equal(t, Judgment, js[0].Kind)
	require.Equal(t, []string{"s1-1"}, js[0].Primaries)
	require.Equal(t, first.ID, js[0].Card)
	require.Contains(t, js[0].What, first.ID, "it names the card")
	require.Contains(t, js[0].What, named, "and the branch")
	require.Equal(t, []string{"ack", "rework with a fix", "drop"}, js[0].Decisions)
	require.Empty(t, w.notesOf(NReadBroken), "no verdict, no rework")

	pr := w.s.Work.Card("s1-1")
	for range 3 {
		dealReads(t, w, seats)
		askReaders(t, w, seats)
	}
	require.Empty(t, readCardsOf(w, "s1-1"), "not asked again while the branch is missing")
	require.Zero(t, ReadsWanted(w.s, pr))
	require.Len(t, w.notesOf(NReadBranchMissing), 1, "told once")

	w.must(Ack(w.s, AckReq{Notes: []string{js[0].ID}, Reason: "the branch is pushed", Who: "coordinator"}))
	dealReads(t, w, seats)
	require.Len(t, readCardsOf(w, "s1-1"), 1, "acked, the read is asked again")
}
