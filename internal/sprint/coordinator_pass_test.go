package sprint_test

import (
	"context"
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
}

// passPongBefore is how long before the rig starts each friend's session last answered.
const passPongBefore = 5 * time.Minute

func newPassRig(t *testing.T) *passRig {
	t.Helper()
	r := &passRig{holdRig: newHoldRig(t, 1, 1), pongs: map[string]time.Time{"amy": holdT0.Add(-passPongBefore), "bob": holdT0.Add(-passPongBefore)}}
	r.tick(0)
	return r
}

// tick moves the clock by d, beats everyone (each friend with her pong, and her session
// active, as on a long turn: the stall ladder, friend_stall.go, leaves her be, and the
// pass alone judges her) and runs one tick.
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
		_, err := r.st.FriendBeatPong(r.ctx, f, sprint.FriendReport{Active: r.clock()}, nil, pong)
		require.NoError(r.t, err)
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

	// the friend's card is dealt to a friend (working at once), and the machine card is
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

	// closed episodes stay closed: no push for a judgment that no longer holds
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

// starveRig is the night of 2026-10-05 on the twin store: friends amy and zhi (width 2,
// pro), m1 and m2 up with no card of theirs, and two cards whose WHO line prefers zhi
// while she is down. Only the friends in up beat.
type starveRig struct {
	*holdRig
	up map[string]bool
}

func newStarveRig(t *testing.T) *starveRig {
	t.Helper()
	m := store.NewMem()
	h := &holdRig{t: t, ctx: context.Background(), now: holdT0}
	n := 0
	h.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		NewID: func() string { h.mu.Lock(); defer h.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, h.st.Init(h.ctx))
	require.NoError(t, m.RowsAdd(h.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
	require.NoError(t, m.SetCoordinator(h.ctx, "coordinator"))
	_, _, _, err := h.st.SyncFriends(h.ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "pro"}, {Name: "zhi", Width: 2, Class: "pro"}})
	require.NoError(t, err)
	r := &starveRig{holdRig: h, up: map[string]bool{"amy": true}}
	r.beat()
	h.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	h.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: 2}))
	h.must(store.AddStep(sprint.AddReq{Stream: "z", Cards: []sprint.CardAdd{
		{ID: "z-1", Brief: friendsBrief("friend zhi")}, {ID: "z-2", Brief: friendsBrief("friend zhi")}}}))
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.NoError(t, err)
	return r
}

// tick moves the clock by d, beats the members, the readers and the friends up, and runs
// one tick.
func (r *starveRig) tick(d time.Duration) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// beat is one beat of the members, the readers and the friends up.
func (r *starveRig) beat() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for f := range r.up {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
	}
}

// judgments is the notes of the kind and type written, whatever they say.
func (r *starveRig) judgments(typ string) []sprint.Note {
	var out []sprint.Note
	for _, n := range (&passRig{holdRig: r.holdRig}).notes() {
		if n.Kind == sprint.Judgment && n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

// Zhi's night (the owner, 2026-10-05 ~11:05 PM ET: "How is it that you missed Zhi having
// zero cards? Seems bad."): her pinned cards went to amy while she was down, each a
// judgment saying why and where; she came up to an empty row while they sat dealt and
// unstarted on amy's row, and ten minutes on the pass told the coordinator once, naming
// her, the cards and where they sit, with the remedies; raised again ten minutes later;
// closed once her row holds cards.
func TestAnIdleUpFriendWhileCardsWaitElsewhereIsToldOnce(t *testing.T) {
	t.Parallel()
	r := newStarveRig(t)
	zhi, amy := sprint.FriendRow("zhi"), sprint.FriendRow("amy")
	pass := &passRig{holdRig: r.holdRig}

	// zhi is down: her cards go to amy, working at once, and each rotation is a judgment
	r.tick(time.Second)
	s := r.snap()
	require.ElementsMatch(t, []string{"z-1.w1", "z-2.w1"}, onRow(s, amy, "z", sprint.Working), "zhi is down: her preferred cards go to amy")
	rot := r.judgments(sprint.NPinRotated)
	require.Len(t, rot, 2, "each rotation of a pin is a judgment, never silent")
	for _, n := range rot {
		assert.Equal(t, []string{zhi}, n.Primaries, "about the friend the card prefers")
		assert.Contains(t, n.What, "WHO: friend zhi")
		assert.Contains(t, n.What, "she is down", "why she did not take it")
		assert.Contains(t, n.What, "is on "+amy+"'s row", "whose row it is on now")
		assert.Contains(t, n.Decisions, "keep")
	}
	assert.Contains(t, rot[0].Decisions[len(rot[0].Decisions)-1], "friend take amy z-")

	// zhi comes up: her row is empty and amy's cards are working, unstarted; the level
	// moves no working card, so she sits at zero
	r.up["zhi"] = true
	r.tick(time.Second)
	s = r.snap()
	assert.Empty(t, onRow(s, zhi, "z", sprint.Ready, sprint.Working), "nothing moves the working cards to her")
	r.tick(9*time.Minute + 58*time.Second)
	assert.Nil(t, pass.open(sprint.NFriendStarved, zhi), "an empty row under ten minutes is not told")

	// ten minutes: told once, naming her, the cards and where they sit, with the remedies
	r.tick(2 * time.Second)
	j := pass.open(sprint.NFriendStarved, zhi)
	require.NotNil(t, j, "an up friend with an empty row for ten minutes while cards she could do wait is a judgment")
	assert.Contains(t, j.What, "friend zhi is up")
	assert.Contains(t, j.What, "2 dealt and unstarted on "+amy+" (z-1, z-2)")
	assert.Contains(t, j.Decisions, "friend take amy z-1 z-2", "deal them to her")
	assert.Contains(t, j.Decisions, "friend take amy --all-unstarted", "take the other row")
	assert.Contains(t, j.Decisions, "keep")
	assert.Nil(t, pass.open(sprint.NFriendStarved, amy), "amy holds cards: not starved")
	r.tick(5 * time.Minute)
	assert.Len(t, r.judgments(sprint.NFriendStarved), 1, "told once an episode")
	assert.Equal(t, 0, pass.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendStarved), "not again within ten minutes")

	// ten minutes on: raised again in place, a push to the coordinator
	r.tick(5 * time.Minute)
	assert.Len(t, r.judgments(sprint.NFriendStarved), 1, "raised again in place, never a second judgment")
	assert.Equal(t, 1, pass.count(sprint.Happened, sprint.NRaisedAgain, sprint.NFriendStarved))
	j = pass.open(sprint.NFriendStarved, zhi)
	require.NotNil(t, j)
	assert.Equal(t, 1, j.Before)

	// the coordinator takes amy's row back: the deal gives the cards to zhi, and the
	// judgment closes; her own cards coming home are no rotation
	r.must(store.FriendTakeStep(sprint.FriendTakeReq{Friend: "amy", All: true, Who: "coordinator"}))
	r.tick(time.Second)
	s = r.snap()
	assert.ElementsMatch(t, []string{"z-1.w1", "z-2.w1"}, onRow(s, zhi, "z", sprint.Ready, sprint.Working), "the deal gives her preferred cards to her")
	assert.Nil(t, pass.open(sprint.NFriendStarved, zhi), "her row holds cards: closed")
	assert.Len(t, r.judgments(sprint.NPinRotated), 2, "a card dealt to its friend is no rotation")
	_, had := s.Fleet.Prop(sprint.PropFriendEmptySince("zhi"))
	v, _ := s.Fleet.Prop(sprint.PropFriendEmptySince("zhi"))
	assert.True(t, !had || v == "", "her empty row ended")
}

// A friend up with an empty row and nothing she could do waiting elsewhere is not starved.
func TestAnEmptyFriendWithNothingWaitingIsNotStarved(t *testing.T) {
	t.Parallel()
	r := newStarveRig(t)
	zhi := sprint.FriendRow("zhi")
	pass := &passRig{holdRig: r.holdRig}
	r.tick(time.Second)
	// amy finishes both cards: nothing waits anywhere
	s := r.snap()
	for _, c := range s.Fleet.Cell(sprint.FriendRow("amy"), sprint.Working) {
		r.must(store.FinishStep(sprint.FinishReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Head: "abc", Who: c.Row}))
	}
	r.up["zhi"] = true
	r.tick(time.Second)
	r.tick(11 * time.Minute)
	assert.Nil(t, pass.open(sprint.NFriendStarved, zhi), "nothing she could do waits: not starved")
	assert.Empty(t, r.judgments(sprint.NFriendStarved))
}
