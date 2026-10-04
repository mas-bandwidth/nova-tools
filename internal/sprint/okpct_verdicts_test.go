package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friends' and the fleet's ok% is the readers' (docs/SPEC-SPRINT.md section 1; the
// owner, 2026-10-04: "trust but VERIFY"; "I want to trust the ok%"): a worker's own word
// moves a work card to finished and counts nothing, a reader's verdict moves it to ok or
// failed, and a friend's card past its deadline unfinished is redealt, counted in redealt
// on her row, never failed.
func TestOkPercentCountsReaderVerdictsOnlyAndADeadlineIsARedeal(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	amy := FriendRow("amy")
	seat := FriendSeat{Name: "amy", Width: 4, Status: Up}
	dealWith(w, seat)
	require.Equal(t, 4, w.s.Fleet.Count(amy, Working))
	counts := func() [4]int {
		return [4]int{w.s.Fleet.Count(amy, Finished), w.s.Fleet.Count(amy, DoneOK), w.s.Fleet.Count(amy, DoneFailed), w.s.Fleet.Count(amy, Redealt)}
	}

	// her word alone, LAND or HOLD, counts nothing: both cards wait in finished
	finish := func(id string, failed bool) {
		r := FinishReq{Sel: Sel{IDs: []string{id}}, As: amy, Gens: gensOf(w.s, id), Head: "abc"}
		if failed {
			r = FinishReq{Sel: Sel{IDs: []string{id}}, As: amy, Gens: gensOf(w.s, id), Failed: true, Report: "friend amy HOLD: the gate is red"}
		}
		w.must(Finish(w.s, r))
	}
	finish("s1-1.w1", false)
	finish("s1-2.w1", false)
	finish("s1-3.w1", true)
	assert.Equal(t, [4]int{3, 0, 0, 0}, counts(), "finished, ok, failed, redealt: a worker's own word is no verdict")
	w.part(TickVerdicts, TickReq{})
	assert.Equal(t, [4]int{3, 0, 0, 0}, counts(), "no read, no verdict")

	// the readers read: s1-1 ok from the two it needs, s1-2 broken by one
	w.must(Ask(w.s, AskReq{}))
	for _, rc := range readsAt(w.s, w.s.Work.Card("s1-1"), 1) {
		w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	broken := readsAt(w.s, w.s.Work.Card("s1-2"), 1)[0]
	w.must(Read(w.s, ReadReq{As: broken.Row, Verdict: "broken", Finding: "internal/x.go:1: off by one", Sel: Sel{IDs: []string{broken.ID}}}))
	w.part(TickVerdicts, TickReq{})
	assert.Equal(t, [4]int{1, 1, 1, 0}, counts(), "the readers' verdicts are the ok and the failed; the HOLD no reader read stays finished")
	assert.Equal(t, DoneOK, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, DoneFailed, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Finished, w.s.Fleet.Card("s1-3.w1").Col)

	// s1-4 is never finished: past its deadline it is redealt, never failed and never judged
	w.tick(DeadlineUnfinished + time.Minute)
	p, _ := TickDeadlines(w.s, TickReq{})
	for _, n := range p.Notes {
		assert.NotEqual(t, NWorkLate, n.Type, "a friend's card past its deadline is no late judgment: %s", n.What)
	}
	w.part(TickFriendRedeal, TickReq{})
	assert.Equal(t, [4]int{1, 1, 1, 1}, counts(), "the card past its deadline is counted redealt on her row")
	assert.Equal(t, Redealt, w.s.Fleet.Card("s1-4.w1").Col)
	assert.Equal(t, Ready, w.s.StateOf("s1-4"), "its primary waits ready for the next deal")
	dealWith(w, seat)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-4.w2").Col, "the next deal deals its next attempt")
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Redealt), "the redeal is counted once")
	assert.Empty(t, Check(w.s, nil), "what is always true holds")

	// her late report of the redealt card changes nothing
	late := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-4.w1"}}, As: amy, Gens: gensOf(w.s, "s1-4.w1"), Head: "abc"})
	assert.Empty(t, late.Units, "a redealt card is finished by no one")
}
