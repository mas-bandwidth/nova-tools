package sprint_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The coordinator's pass (docs/SPEC-SPRINT.md section 8, "The coordinator's pass";
// tla/CoordinatorPass.tla), on the twin store with a fake clock: a deaf friend, an idle
// friend and a judgment late on the coordinator, each raised once, raised again with a
// push every ten minutes of running time while it holds, and closed when it stops.

// passRig is the hold rig with each friend's session pong kept on her beat. Each
// friend's session last answered five minutes before the rig starts, so it is deaf
// (FriendDeafAfter, fifteen minutes) ten minutes and more into the rig.
type passRig struct {
	*holdRig
	pongs map[string]time.Time
	// answered is each friend's last wake ping answer the rig recorded (friend health):
	// the store renews only on a newer one.
	answered map[string]time.Time
	// proved is each friend's last session proof the rig put on her beat.
	proved map[string]time.Time
}

// passPongBefore is how long before the rig starts each friend's session last answered.
const passPongBefore = 5 * time.Minute

func newPassRig(t *testing.T) *passRig {
	t.Helper()
	r := &passRig{holdRig: newHoldRig(t, 1, 1), pongs: map[string]time.Time{"amy": holdT0.Add(-passPongBefore), "bob": holdT0.Add(-passPongBefore)}, answered: map[string]time.Time{}, proved: map[string]time.Time{}}
	rows, err := r.st.FriendRows(r.ctx, r.clock())
	require.NoError(t, err)
	for _, row := range rows {
		if row.Health != nil {
			r.answered[row.Name] = row.Health.Seen
		}
	}
	r.tick(0)
	r.startFriends()
	return r
}

// startFriends is each friend's start of every card dealt ready on her row: her own take
// (take --as friend.<name>), as her daemon starts a card, so it is working only once she
// starts it (docs/SPEC-SPRINT.md section 1, a friend's card is working once she starts it).
func (r *holdRig) startFriends() {
	r.t.Helper()
	for _, row := range r.snap().Fleet.Rows() {
		if _, ok := sprint.FriendOfRow(row); ok {
			name, _ := sprint.FriendOfRow(row)
			req := sprint.FriendStartReq{Friend: name, Gens: map[string]int{}}
			for _, c := range r.snap().Fleet.Cell(row, sprint.Ready) {
				req.IDs, req.Gens[c.ID] = append(req.IDs, c.ID), max(c.Int("gen"), 1)
			}
			if len(req.IDs) > 0 {
				r.must(store.FriendStartStep(req)) // her start receipt, as friend sync reads it
			}
		}
	}
}

// tick moves the clock by d, beats everyone (each friend with her pong, and her session
// active, as on a long turn: the stall ladder, friend_stall.go, leaves her be, and the
// pass alone judges her), records each pong as a wake ping her session answered (her
// beat is no evidence: she is up only on that answer) and runs one tick.
func (r *passRig) tick(d time.Duration) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for f, pong := range r.pongs {
		if pong.After(r.proved[f]) {
			// her daemon asked a check and her session answered it at pong (sprint.ProveBeat)
			r.mu.Lock()
			now := r.now
			r.now = pong
			r.mu.Unlock()
			nonce := fmt.Sprintf("n%d", pong.Unix())
			_, _, err := r.st.FriendBeatProof(r.ctx, f, sprint.FriendReport{Active: pong}, nil, sprint.BeatWords{Run: "run1", Check: nonce, Pong: nonce})
			require.NoError(r.t, err)
			r.mu.Lock()
			r.now = now
			r.mu.Unlock()
			r.proved[f] = pong
		}
		_, err := r.st.FriendBeatReport(r.ctx, f, sprint.FriendReport{Active: r.clock()}, nil)
		require.NoError(r.t, err)
		if pong.After(r.answered[f]) {
			_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: pong, Generation: sprint.FirstSeatGeneration}, "")
			require.NoError(r.t, err)
			r.answered[f] = pong
		}
	}
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// clock is the rig's clock now.
func (r *passRig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// notes is every note the sprint wrote.
func (r *passRig) notes() []sprint.Note {
	r.t.Helper()
	notes, _, err := r.st.B.(*store.Mem).NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	return notes
}

// count is how many notes of the kind and type the sprint wrote whose what says the word.
func (r *passRig) count(kind, typ, word string) int {
	n := 0
	for _, x := range r.notes() {
		if x.Kind == kind && x.Type == typ && strings.Contains(x.What, word) {
			n++
		}
	}
	return n
}

// open is the open judgment of the type on the subject, nil when none is.
func (r *passRig) open(typ, subject string) *sprint.Note {
	for _, o := range r.snap().Open {
		if o.Note.Type == typ && o.Subject() == subject {
			n := o.Note
			return &n
		}
	}
	return nil
}

// tickEnds is how many tick-end notes (the coordinator's wake: inbox --wait) were written.
func (r *passRig) tickEnds() int { return r.count(sprint.Happened, sprint.NTickEnd, "") }

func TestTheMachineRemindsTheCoordinatorOfADeafOrIdleFriendEveryTenMinutes(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	amy, bob := sprint.FriendRow("amy"), sprint.FriendRow("bob")
	behind := sprint.StreamSubject("")

	// the friend's card is dealt to a friend (working once she starts it), and the machine card is
	// taken by m1 and comes back failed: a judgment that waits on the coordinator
	s := r.snap()
	fc := s.Fleet.Card(s.Work.Card("f1-1").F("work"))
	require.NotNil(t, fc, "the friend's card is dealt")
	require.Equal(t, sprint.Working, fc.Col)
	holder, _ := sprint.FriendOfRow(fc.Row)
	wc := r.takeOne("m1")
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: r.snap().Fleet.Card(wc).Int("gen")}, Failed: true, Who: "m1"}))
	require.NotNil(t, r.open(sprint.NWorkFailed, "s1-1"), "failed work is a judgment")

	// bob's session answers every ping from here on; amy's last answered 5 minutes before t0
	fresh := func() { r.pongs["bob"] = r.clock() }

	// 9 minutes: nothing yet
	fresh()
	r.tick(9 * time.Minute)
	assert.Nil(t, r.open(sprint.NFriendDeaf, amy), "a pong 14 minutes old is not deaf")
	assert.Nil(t, r.open(sprint.NCoordinatorBehind, behind), "a judgment 9 minutes old is not late")

	// 10m30s: amy deaf, raised once; the failed judgment late, named by its overdue line
	// (the first reminder) and not yet by the pass
	fresh()
	r.tick(90 * time.Second)
	deaf := r.open(sprint.NFriendDeaf, amy)
	require.NotNil(t, deaf, "amy's session has not answered for over 15 minutes: deaf")
	assert.Contains(t, deaf.What, "friend amy")
	assert.Contains(t, deaf.What, "15m3", "her pong age")
	assert.Contains(t, deaf.What, "--wake", "the remedy, a wake note")
	assert.Contains(t, deaf.What, "SPEC-FRIEND.md", "then the debug steps")
	assert.Nil(t, r.open(sprint.NFriendDeaf, bob), "bob's session answers")
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NOverdue, sprint.NWorkFailed), "the overdue line names the late judgment")
	assert.Nil(t, r.open(sprint.NCoordinatorBehind, behind), "the overdue line is the first reminder, not the pass")
	assert.Nil(t, r.open(sprint.NFriendIdle, sprint.FriendRow(holder)), "her card is 10 minutes old: not idle")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendDeaf, "friend amy"))
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, ""))

	// 15m30s and 20m: still held, not raised again before ten minutes of running time
	fresh()
	r.tick(5 * time.Minute)
	fresh()
	r.tick(4*time.Minute + 59*time.Second)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendDeaf, "friend amy"), "one judgment an episode")
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, ""), "not again within 10 minutes")

	assert.Nil(t, r.open(sprint.NCoordinatorBehind, behind), "the late judgment's overdue line is under 10 minutes old")

	// 20m31s: ten minutes on, deaf raised again, a push to the coordinator that wakes her;
	// the late judgment's overdue line is ten minutes old: the pass raises behind
	ends := r.tickEnds()
	fresh()
	r.tick(time.Second)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendDeaf, "friend amy"), "raised again in place, never a second judgment")
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendDeaf))
	late := r.open(sprint.NCoordinatorBehind, behind)
	require.NotNil(t, late, "a judgment waits on the coordinator past its deadline, 10 minutes after its overdue line")
	assert.Contains(t, late.What, "1 judgments wait past their deadline")
	assert.Contains(t, late.What, "1 "+sprint.NWorkFailed)
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NOverdue, sprint.NWorkFailed), "the overdue line is written once")
	for _, n := range r.notes() {
		if n.Type == sprint.NRaisedAgain {
			assert.Equal(t, "coordinator", n.To, "the push is the coordinator's")
		}
	}
	assert.Greater(t, r.tickEnds(), ends, "the push wakes inbox --wait")
	deaf = r.open(sprint.NFriendDeaf, amy)
	require.NotNil(t, deaf)
	assert.Equal(t, 1, deaf.Before, "the judgment counts its raises again")
	assert.Contains(t, deaf.What, "25m3", "with her latest pong age")

	// 31m: the friend holding her card has finished none in 30 minutes: idle, naming the card
	fresh()
	r.tick(10*time.Minute + 29*time.Second)
	idle := r.open(sprint.NFriendIdle, sprint.FriendRow(holder))
	require.NotNil(t, idle, "a friend holding working cards with no finish in 30 minutes is idle")
	assert.Contains(t, idle.What, fc.ID)
	assert.Contains(t, idle.What, "no finish yet")
	assert.Equal(t, 2, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendDeaf), "deaf raised again at 30m31s")
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NCoordinatorBehind), "behind raised again at 30m31s")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NCoordinatorBehind, ""), "one behind judgment an episode")

	// amy's session answers: deaf closes; the failed card is dropped: late closes
	r.pongs["amy"] = r.clock()
	r.must(store.DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "the pass test", Who: "coordinator"}))
	fresh()
	r.tick(time.Second)
	assert.Nil(t, r.open(sprint.NFriendDeaf, amy), "an answered ping closes deaf")
	assert.Nil(t, r.open(sprint.NCoordinatorBehind, behind), "nothing late closes behind")
	assert.NotNil(t, r.open(sprint.NFriendIdle, sprint.FriendRow(holder)), "idle holds while her card is unfinished")

	// 41m: idle raised again; then her card finishes (working to done): idle closes
	r.pongs["amy"] = r.clock()
	fresh()
	r.tick(10 * time.Minute)
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendIdle))
	s = r.snap()
	fc = s.Fleet.Card(fc.ID)
	r.must(store.FinishStep(sprint.FinishReq{As: fc.Row, Sel: sprint.Sel{IDs: []string{fc.ID}}, Gens: map[string]int{fc.ID: fc.Int("gen")}, Head: "abc", Who: fc.Row}))
	r.pongs["amy"] = r.clock()
	fresh()
	r.tick(time.Second)
	assert.Nil(t, r.open(sprint.NFriendDeaf, amy), "amy answers")
	assert.Nil(t, r.open(sprint.NFriendIdle, sprint.FriendRow(holder)), "a finish closes idle")

	// closed episodes stay closed: no push for a judgment that no longer holds. The status
	// judgments of the friends' comings and goings are answered first, as the coordinator
	// answers them once their steps are run: one left waiting is the behind judgment's
	acked := map[string]bool{}
	for _, o := range r.snap().Open {
		if o.Note.Type == sprint.NStatus && o.Note.Kind == sprint.Judgment && !acked[o.Note.ID] {
			acked[o.Note.ID] = true
			r.must(store.AckStep(sprint.AckReq{Notes: []string{o.Note.ID}, Reason: "her steps are run", Who: "coordinator"}))
		}
	}
	pushes := r.count(sprint.Happened, sprint.NRaisedAgain, "")
	r.pongs["amy"] = r.clock()
	fresh()
	r.tick(10 * time.Minute)
	assert.Equal(t, pushes, r.count(sprint.Happened, sprint.NRaisedAgain, ""), "nothing holds, nothing is pushed")

	// a second episode is raised again from the start: amy goes quiet once more
	fresh()
	r.tick(5*time.Minute + time.Second)
	assert.Equal(t, 2, r.count(sprint.Judgment, sprint.NFriendDeaf, "friend amy"), "a new episode, a new judgment")
}

// The pass's quiet ways: a held friend is not judged; an acknowledged judgment is not
// raised again while its condition stands; the friend-finish window is the sprint's
// setting (set --friend-finish).
func TestThePassSparesAHeldFriendAnAcknowledgedJudgmentAndKeepsTheFinishSetting(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	s := r.snap()
	fc := s.Fleet.Card(s.Work.Card("f1-1").F("work"))
	require.NotNil(t, fc)
	holder, _ := sprint.FriendOfRow(fc.Row)
	other := map[string]string{"amy": "bob", "bob": "amy"}[holder]
	r.hold(sprint.HoldReq{Names: []string{other}})
	set := store.SetStep(sprint.SetReq{Who: "coordinator"})
	plan := set.Plan
	set.Plan = func(s *sprint.Snapshot) sprint.Plan { return sprint.WithFriendFinish(plan(s), s, "45m") }
	r.must(set)

	r.tick(10*time.Minute + 30*time.Second)
	deaf := r.open(sprint.NFriendDeaf, sprint.FriendRow(holder))
	require.NotNil(t, deaf, "%s's session is deaf", holder)
	assert.Nil(t, r.open(sprint.NFriendDeaf, sprint.FriendRow(other)), "a held friend is not judged")
	r.must(store.AckStep(sprint.AckReq{Notes: []string{deaf.ID}, Reason: "she is on a long turn", Who: "coordinator"}))

	r.tick(10 * time.Minute)
	r.tick(10 * time.Minute)
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendDeaf), "an acknowledged judgment is not pushed")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendDeaf, "friend "+holder), "nor written again while it stands")
	assert.Nil(t, r.open(sprint.NFriendIdle, fc.Row), "30m30s with no finish is inside the 45m window")

	r.tick(15 * time.Minute)
	assert.NotNil(t, r.open(sprint.NFriendIdle, fc.Row), "45m30s with no finish is idle")
}

// A quiet friend whose session answers is never judged deaf: her daemon asks the session
// after ten minutes with no word and waits five for the answer, so her proof is at most
// FriendProofLive old. Past that it has lapsed, and the coordinator is told once, as a
// judgment naming her and the remedy, never only in her daemon's log.
func TestAQuietFriendWhoseSessionAnswersIsNotDeafAndALapsedProofIsToldOnce(t *testing.T) {
	t.Parallel()
	require.Equal(t, sprint.FriendProofLive, sprint.FriendDeafAfter, "deaf is a lapsed proof")
	r := newPassRig(t)
	amy := sprint.FriendRow("amy")
	r.pongs["amy"], r.pongs["bob"] = r.clock(), r.clock()

	// asked after ten quiet minutes, answered at the five-minute bound: not deaf
	r.tick(10 * time.Minute)
	r.pongs["bob"] = r.clock()
	r.tick(5 * time.Minute)
	assert.Nil(t, r.open(sprint.NFriendDeaf, amy), "a proof FriendProofLive old is live: not deaf")
	assert.Equal(t, 0, r.count(sprint.Judgment, sprint.NFriendDeaf, ""), "a quiet friend whose session answers is never told")

	// one second past the bound: her proof has lapsed, told once as a judgment
	r.pongs["bob"] = r.clock()
	r.tick(time.Second)
	deaf := r.open(sprint.NFriendDeaf, amy)
	require.NotNil(t, deaf, "a proof past FriendProofLive has lapsed: deaf")
	assert.Equal(t, sprint.Judgment, deaf.Kind, "the coordinator is told as a judgment")
	assert.Contains(t, deaf.What, "--wake", "with the remedy")
	assert.Nil(t, r.open(sprint.NFriendDeaf, sprint.FriendRow("bob")), "bob's session answers")
	r.pongs["bob"] = r.clock()
	r.tick(time.Minute)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendDeaf, "friend amy"), "told once an episode")
}

// An up friend with an empty row, while a card she could do sits unstarted on the
// other friend's row, is told once: not before ten minutes, one judgment at ten
// minutes naming her, the card, where it sits and the offers, raised again in
// place one pass later, and closed when the card is finished.
func TestAnIdleUpFriendWhileCardsWaitElsewhereIsToldOnce(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	s := r.snap()
	fc := s.Fleet.Card(s.Work.Card("f1-1").F("work"))
	require.NotNil(t, fc, "the friend's card is dealt")
	require.Equal(t, sprint.Working, fc.Col)
	holder, ok := sprint.FriendOfRow(fc.Row)
	require.True(t, ok)
	idle := map[string]string{"amy": "bob", "bob": "amy"}[holder]
	require.Zero(t, r.snap().Fleet.Count(sprint.FriendRow(idle), sprint.Ready)+r.snap().Fleet.Count(sprint.FriendRow(idle), sprint.Working), "her row is empty")
	row := sprint.FriendRow(idle)
	fresh := func() { r.pongs[holder], r.pongs[idle] = r.clock(), r.clock() }

	assert.Nil(t, r.open(sprint.NFriendEmpty, row), "the empty row has just been seen: the ten minutes start now")
	assert.Equal(t, 0, r.count(sprint.Judgment, sprint.NFriendEmpty, ""))

	fresh()
	r.tick(9*time.Minute + 59*time.Second)
	assert.Nil(t, r.open(sprint.NFriendEmpty, row), "nine minutes and fifty-nine seconds is not ten")
	assert.Equal(t, 0, r.count(sprint.Judgment, sprint.NFriendEmpty, ""))

	fresh()
	r.tick(time.Second)
	j := r.open(sprint.NFriendEmpty, row)
	require.NotNil(t, j, "an up friend empty for ten minutes while a card she could do waits is told")
	assert.Equal(t, sprint.Judgment, j.Kind)
	assert.Contains(t, j.What, "friend "+idle)
	assert.Contains(t, j.What, sprint.EmptyRowAfter.String())
	assert.Contains(t, j.What, fc.ID)
	assert.Contains(t, j.What, fc.Row+":"+fc.Col)
	assert.Contains(t, j.What, "unstarted")
	assert.Contains(t, j.What, "deal them to her")
	assert.Contains(t, j.What, "friend take the other row")
	assert.Contains(t, j.What, "keep")
	assert.Contains(t, j.Decisions, "deal them to "+idle)
	assert.Contains(t, j.Decisions, "friend take "+holder+" --all-unstarted")
	assert.Contains(t, j.Decisions, "keep")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendEmpty, "friend "+idle), "told once")

	fresh()
	r.tick(9*time.Minute + 59*time.Second)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendEmpty, ""), "still the one judgment")
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendEmpty), "not again within ten minutes")

	ends := r.tickEnds()
	fresh()
	r.tick(time.Second)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendEmpty, "friend "+idle), "raised again in place")
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendEmpty))
	again := r.open(sprint.NFriendEmpty, row)
	require.NotNil(t, again)
	assert.Equal(t, 1, again.Before, "the judgment counts its raises again")
	assert.Greater(t, r.tickEnds(), ends, "the push wakes inbox --wait")

	s = r.snap()
	fc = s.Fleet.Card(fc.ID)
	require.NotNil(t, fc)
	r.must(store.FinishStep(sprint.FinishReq{As: fc.Row, Sel: sprint.Sel{IDs: []string{fc.ID}}, Gens: map[string]int{fc.ID: fc.Int("gen")}, Head: "abc", Who: fc.Row}))
	fresh()
	r.tick(time.Second)
	assert.Nil(t, r.open(sprint.NFriendEmpty, row), "nothing she could do is waiting: the judgment closes")
	pushes := r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendEmpty)
	fresh()
	r.tick(10 * time.Minute)
	assert.Equal(t, pushes, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendEmpty), "a closed episode is not pushed")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NFriendEmpty, ""))
}

// A named pin the deal places on another friend's row is a judgment in that
// step: why the pinned friend did not take it, and whose row holds the card.
// The pass keeps that one note, raises it again in place, and closes it when
// the card is finished. A hard pin is not rotated and is not this judgment.
func TestAPinnedCardRotatedOffItsFriendIsJudgedOnce(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "she is away"})
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{{ID: "f1-2", Brief: friendsBrief("friend amy")}}}))
	// the first tick starts her pin clock and holds the card for her inside the bound
	r.pongs["bob"] = r.clock()
	r.tick(time.Second)
	// past the bound the clock waives the pin and the card rotates to bob, which
	// is the judgment this test reads
	r.pongs["amy"] = r.clock().Add(31 * time.Minute)
	r.pongs["bob"] = r.clock().Add(31 * time.Minute)
	r.tick(31 * time.Minute)
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card("f1-2").F("work"))
	require.NotNil(t, wc, "the pin was dealt")
	require.Equal(t, sprint.FriendRow("bob"), wc.Row, "her held pin is waived past the bound, so it rotates to bob")
	j := r.open(sprint.NPinIgnored, "f1-2")
	require.NotNil(t, j, "a pin placed on someone else's row is a judgment")
	assert.Contains(t, j.What, wc.ID)
	assert.Contains(t, j.What, "pinned to amy")
	assert.Contains(t, j.What, "she is held")
	assert.Contains(t, j.What, wc.Row+":"+wc.Col)
	assert.Contains(t, j.Decisions, "friend take bob "+wc.ID+" --reason pinned to amy")
	assert.Contains(t, j.Decisions, "keep")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NPinIgnored, "f1-2"))

	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{{ID: "f1-3", Brief: friendsBrief("only friend amy")}}}))
	r.pongs["bob"] = r.clock()
	r.tick(time.Second)
	assert.Equal(t, sprint.Ready, r.snap().Work.Card("f1-3").Col, "a hard pin waits for her")
	assert.Nil(t, r.open(sprint.NPinIgnored, "f1-3"), "a hard pin is not rotated, and is not this judgment")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NPinIgnored, ""), "the rotated pin is still the one judgment")

	// the hard-pin tick above already took one second of the ten minutes
	r.pongs["bob"] = r.clock()
	r.tick(9*time.Minute + 58*time.Second)
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NPinIgnored))
	r.pongs["bob"] = r.clock()
	r.tick(time.Second)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NPinIgnored, "f1-2"), "raised again in place")
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NPinIgnored))
	again := r.open(sprint.NPinIgnored, "f1-2")
	require.NotNil(t, again)
	assert.Equal(t, 1, again.Before)

	r.startFriends()
	s = r.snap()
	wc = s.Fleet.Card(wc.ID)
	require.NotNil(t, wc)
	r.must(store.FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: "abc", Who: wc.Row}))
	r.pongs["bob"] = r.clock()
	r.tick(time.Second)
	assert.Nil(t, r.open(sprint.NPinIgnored, "f1-2"), "the card is finished: the judgment closes")
}

// A named pin the friends do not take, so the fleet deals it, is still a
// judgment. The deal writes no friend unit for it; the pass raises the one
// note from the row the card landed on.
func TestAPinnedCardTheFleetTookIsJudgedOnce(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "both away"})
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{{ID: "f1-9", Brief: friendsBrief("friend amy")}}}))
	// the first tick starts her pin clock and holds the card inside the bound
	r.tick(time.Second)
	// her pin is waived when the clock runs past the bound; every friend is held,
	// so the fleet deals it
	r.pongs["amy"] = r.clock().Add(31 * time.Minute)
	r.pongs["bob"] = r.clock().Add(31 * time.Minute)
	r.tick(31 * time.Minute)
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card("f1-9").F("work"))
	require.NotNil(t, wc, "no friend is up, so the fleet deals the pin")
	require.False(t, sprint.IsFriendRow(wc.Row), "it sits on a machine, %s", wc.Row)
	j := r.open(sprint.NPinIgnored, "f1-9")
	require.NotNil(t, j, "the pass tells the coordinator the pin was dealt away")
	assert.Contains(t, j.What, wc.ID)
	assert.Contains(t, j.What, "pinned to amy")
	assert.Contains(t, j.What, "she is held")
	assert.Contains(t, j.What, wc.Row+":"+wc.Col)
	assert.NotContains(t, j.Decisions, "friend take")
	assert.Contains(t, j.Decisions, "keep")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NPinIgnored, "f1-9"))
	r.tick(time.Second)
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NPinIgnored, "f1-9"), "the pass keeps the one note")
}
