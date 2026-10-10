package sprint

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card is working only once she starts it (docs/SPEC-SPRINT.md section 1, a
// friend's card is working once she starts it; the owner, 2026-10-05: "They are not working
// unless work turns from working to done."). The deal places it ready on her row; her start
// receipt (her beat naming it running, or friend sync reading her job begun: FriendStart)
// moves it to working and stamps its deadline then; a card in working she never started (a
// finish's or a take-back's next, a take) goes back ready; a card dealt and not started
// within the start bound, while she runs no job, is levelled to an eligible friend with an
// idle lane, never back to her.
func TestAFriendCardIsWorkingOnlyOnceTheFriendStartsIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	amy, bob := FriendRow("amy"), FriendRow("bob")
	seats := func(running ...string) []FriendSeat {
		return []FriendSeat{
			{Name: "amy", Width: 3, Status: Up, Class: "flash", Running: running},
			{Name: "bob", Width: 1, Status: Up, Class: "flash"},
		}
	}
	tick := func(running ...string) Plan {
		t.Helper()
		p, _ := TickDeal(w.s, TickReq{Friends: seats(running...)})
		return w.must(p)
	}
	notStarted := func(p Plan) []string {
		var lines []string
		for _, l := range movedLines(p) {
			if strings.Contains(l, "not started") {
				lines = append(lines, l)
			}
		}
		return lines
	}

	// dealt to a friend with three lanes free: three ready on her row, none working, none taken
	tick()
	require.Equal(t, 3, w.s.Fleet.Count(amy, Ready), "a friend's card is dealt ready, whatever her lanes")
	assert.Zero(t, w.s.Fleet.Count(amy, Working), "nothing she has not started is working")
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		wc := w.s.Fleet.Card(id)
		assert.Empty(t, wc.F("taken"), "%s: no deadline runs on a card she has not started", id)
		assert.Empty(t, wc.F(FieldStarted), id)
		assert.Equal(t, Working, w.s.StateOf(wc.F("primary")), "its primary is dealt, as a machine's is")
	}

	// a tick later, with no start receipt, nothing moves
	w.tick(5 * time.Minute)
	assert.Empty(t, movedLines(tick()))
	assert.Zero(t, w.s.Fleet.Count(amy, Working))
	// Give the other two later ready-state stamps, as after a return to ready. This
	// isolates the eligible one-card level path from a batch too large for Bob's lane.
	for _, id := range []string{"s1-2.w1", "s1-3.w1"} {
		w.s.Fleet.Card(id).Fields["untaken_since"] = stamp(w.s.Now)
	}

	// past the start bound, her beat naming nothing running and nothing of hers working: her
	// oldest unstarted card goes to bob, who has an idle lane, ready on his row (not started
	// there either), with one line; the others have no friend with an idle lane to go to
	w.s.Now = t0.Add(FriendStartMaxDefault + 4*time.Minute)
	p := tick()
	moved := w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, bob, moved.Row, "levelled to an eligible friend with an idle lane")
	assert.Equal(t, Ready, moved.Col, "ready on his row until he starts it")
	assert.Equal(t, "2", moved.F("gen"))
	assert.Contains(t, Split(moved.F(FieldFriendsLeft)), "amy", "never back to her")
	lines := notStarted(p)
	require.Len(t, lines, 1, "one log line for the card levelled")
	assert.Contains(t, lines[0], "s1-1.w1 friend.amy:ready -> friend.bob:ready gen=2 (not started 20m0s after its deal, and friend amy names no job running)")
	assert.Equal(t, amy, w.s.Fleet.Card("s1-2.w1").Row, "no other friend has an idle lane")

	// her beat names s1-2.w1 running: the next tick moves it to working, taken now
	w.tick(time.Minute)
	started := w.s.Now
	p = tick("s1-2.w1")
	wc := w.s.Fleet.Card("s1-2.w1")
	require.Equal(t, Working, wc.Col, "her start receipt moves it to working")
	assert.Equal(t, stamp(started), wc.F("taken"), "its deadline runs from her start")
	assert.Equal(t, stamp(started), wc.F("first_taken"))
	assert.Equal(t, "1", wc.F(FieldStarted))
	assert.Empty(t, wc.F("untaken_since"))
	assert.Contains(t, movedLines(p), "s1-2.w1 friend.amy:ready -> working (started: her beat names it running)")

	// past the start window, her beat naming s1-2 running (other work), the deal returned
	// s1-3.w1 to the pool in that tick, never to be dealt back to her (a receipt within the
	// window is the separate test TestAFriendStartReceiptStartsACardWithinTheWindow)
	wc = w.s.Fleet.Card("s1-3.w1")
	require.NotNil(t, wc)
	assert.Equal(t, Withdrawn, wc.Col, "past the window it goes back to the pool")
	assert.Equal(t, amy, wc.Row)
	assert.Equal(t, amy, wc.F(FieldTakenFrom), "not dealt back to her")
	assert.Equal(t, "not started by amy in 20m; back to the pool", wc.F(FieldTakenBack))
	assert.Equal(t, Ready, w.s.StateOf("s1-3"), "its primary is back in the pool")

	// a take moves bob's card to working with no start of his (as a finish's or a
	// take-back's next does): the next tick puts it back ready, untaken, one line
	w.tick(time.Minute)
	w.s.Friends = seats()
	w.must(Take(w.s, TakeReq{As: bob, Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 2}, Who: bob}))
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	w.tick(time.Minute)
	back := w.s.Now
	p = tick("s1-2.w1")
	wc = w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, Ready, wc.Col, "working on a friend's row means started")
	assert.Equal(t, bob, wc.Row)
	assert.Empty(t, wc.F("taken"))
	assert.Empty(t, wc.F("first_taken"))
	assert.Empty(t, wc.F(FieldFriendDeadline))
	assert.Equal(t, stamp(back), wc.F("untaken_since"), "its start bound runs from its return to ready")
	assert.Contains(t, movedLines(p), "s1-1.w1 friend.bob:working -> ready (working with no start of hers: it waits ready until she starts it)")
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Working), "her started card stays working")
	assert.Equal(t, 0, w.s.Fleet.Count(amy, Ready))

	// bob does not start it within the bound from its return. Amy has an idle lane but
	// the card has left her, so it returns to the pool rather than going back to her.
	w.tick(FriendStartMaxDefault + time.Minute)
	p = tick("s1-2.w1")
	assert.Contains(t, strings.Join(notStarted(p), "\n"), "not started by bob in 20m; back to the pool")
	assert.Equal(t, bob, w.s.Fleet.Card("s1-1.w1").Row, "a card never goes back to a friend it left")
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, bob, w.s.Fleet.Card("s1-1.w1").F(FieldTakenFrom))
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, Check(w.s, nil))

	// the start bound is a setting
	w.s.Work.SetProp(PropFriendStartMax, "45m")
	assert.Equal(t, 45*time.Minute, w.s.FriendStartMax())
	w.s.Work.SetProp(PropFriendStartMax, "nonsense")
	assert.Equal(t, FriendStartMaxDefault, w.s.FriendStartMax())
}

// friend sync's start receipt (FriendStart) moves a card ready on her row to working within
// the start window, its deadline from then; a receipt for a card she started already changes
// nothing, and one at another generation or for a card not on her row is refused.
func TestAFriendStartReceiptStartsACardWithinTheWindow(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	amy := FriendRow("amy")
	seats := []FriendSeat{{Name: "amy", Width: 3, Status: Up, Class: "flash"}}
	dealWith(w, seats...)
	require.Equal(t, 3, w.s.Fleet.Count(amy, Ready), "dealt ready, none started")

	w.tick(5 * time.Minute)
	synced := w.s.Now
	p := w.must(FriendStart(w.s, FriendStartReq{Friend: "amy", IDs: []string{"s1-3.w1"}, Gens: map[string]int{"s1-3.w1": 1},
		Why: map[string]string{"s1-3.w1": "a write under jobs/s1-3 after its staging"}}))
	wc := w.s.Fleet.Card("s1-3.w1")
	require.Equal(t, Working, wc.Col, "her start receipt moves it to working")
	assert.Equal(t, stamp(synced), wc.F("taken"), "its deadline runs from the start friend sync read")
	assert.Equal(t, "1", wc.F(FieldStarted))
	assert.Contains(t, movedLines(p), "s1-3.w1 friend.amy:ready -> working (started: a write under jobs/s1-3 after its staging)")

	// a receipt for a card she started already changes nothing; one at another generation, or
	// for a card not on her row, is refused
	assert.Empty(t, FriendStart(w.s, FriendStartReq{Friend: "amy", IDs: []string{"s1-3.w1"}, Gens: map[string]int{"s1-3.w1": 1}}).Units)
	ref := FriendStart(w.s, FriendStartReq{Friend: "amy", IDs: []string{"s1-2.w1", "s1-9.w1"}, Gens: map[string]int{"s1-2.w1": 2, "s1-9.w1": 1}}).Refused
	require.Len(t, ref, 2)
	assert.Contains(t, ref[0].Why, "generation 1, not 2")
	assert.Contains(t, ref[1].Why, "not on friend amy's row")
}

// A friend whose daemon saw a write of hers within the start bound keeps her unstarted cards
// (her start not yet read by friend sync); once her last write is older than the bound, the
// oldest moves.
func TestAFriendWritingWithinTheBoundKeepsHerUnstartedCards(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	seat := func(active time.Time) []FriendSeat {
		return []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash", Active: active}, {Name: "bob", Width: 1, Status: Up, Class: "flash"}}
	}
	w.must(func() Plan { p, _ := TickDeal(w.s, TickReq{Friends: seat(time.Time{})}); return p }())
	require.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row)
	w.s.Now = t0.Add(FriendStartMaxDefault + time.Minute)
	p, _ := TickDeal(w.s, TickReq{Friends: seat(w.s.Now.Add(-time.Minute))})
	w.must(p)
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row, "a write a minute ago: she is at work")
	p, _ = TickDeal(w.s, TickReq{Friends: seat(t0)})
	w.must(p)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "no write within the bound: it moves")
}

// A friend running a job she has started does not keep the rest past the window. The
// start-bound level skips her (a card she started is working), so the deal returns the
// unstarted card to the pool. The same tick cannot place it again.
func TestAFriendRunningAJobReturnsAnUnstartedCard(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash"}, {Name: "bob", Width: 1, Status: Up, Class: "flash"}}
	w.must(func() Plan { p, _ := TickDeal(w.s, TickReq{Friends: seats}); return p }())
	require.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready))
	w.must(FriendStart(w.s, FriendStartReq{Friend: "amy", IDs: []string{"s1-1.w1"}, Gens: map[string]int{"s1-1.w1": 1}}))
	w.s.Now = t0.Add(FriendStartWindowDefault + time.Hour)
	p, _ := TickDeal(w.s, TickReq{Friends: seats})
	w.must(p)
	wc := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, wc)
	assert.Equal(t, Withdrawn, wc.Col, "past the window it goes back to the pool")
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, FriendRow("amy"), wc.F(FieldTakenFrom), "not dealt back to her, and not moved onto bob in this tick")
	assert.Equal(t, Ready, w.s.StateOf("s1-2"), "its primary is back in the pool")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "her started card stays working")
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row)
}

// The start-bound level honours a recipient's work restriction (her nova-config row's
// streams and kinds, docs/SPEC-SPRINT.md section 1, a friend's card): a card dealt and not
// started past the bound moves only to a friend whose restriction holds its primary, and a
// restricted friend with an idle lane is not made its new home.
func TestStartBoundLevellingSkipsARestrictedRecipient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		stream  string
		kind    string
		streams []string
		kinds   []string
		want    string
	}{
		{name: "stream outside the recipient's restriction", stream: "s1", kind: "fix", streams: []string{"security*"}, want: "amy"},
		{name: "kind outside the recipient's restriction", stream: "security-a", kind: "test", kinds: []string{"fix"}, want: "amy"},
		{name: "within the recipient's restriction", stream: "security-a", kind: "fix", streams: []string{"security*"}, kinds: []string{"fix"}, want: "bob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, "reader-a", "reader-b")
			w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
			w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
			id := tc.stream + "-1"
			brief := "c: a restricted level\nREPO: mas-bandwidth/nova-tools\nWHO: friend\nKIND: " + tc.kind + "\n\nThe task."
			w.must(Add(w.s, AddReq{Stream: tc.stream, Cards: []CardAdd{{ID: id, Brief: brief}}}))
			seats := func() []FriendSeat {
				return []FriendSeat{
					{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"},
					{Name: "bob", Width: 1, Status: Up, Class: "flash,pro", Streams: tc.streams, Kinds: tc.kinds},
				}
			}
			w.must(func() Plan { p, _ := TickDeal(w.s, TickReq{Friends: seats()}); return p }())
			require.Equal(t, FriendRow("amy"), w.s.Fleet.Card(id+".w1").Row, "the card is dealt to amy first")

			w.s.Now = t0.Add(FriendStartMaxDefault + time.Minute)
			p, _ := TickDeal(w.s, TickReq{Friends: seats()})
			w.must(p)
			assert.Equal(t, FriendRow(tc.want), w.s.Fleet.Card(id+".w1").Row, "the level honours the recipient's restriction")
		})
	}
}

// startLanes is each friend's start receipt for the oldest cards ready on her row, as many
// as her lanes have free (her width less her working cards): her beat names them running,
// and the tick's start moves take them into working (friendStartUnits).
func startLanes(w *world, seats ...FriendSeat) {
	w.t.Helper()
	for i, f := range seats {
		if f.Status != Up {
			continue
		}
		row := FriendRow(f.Name)
		_, width := friendRoom(f)
		ready := append([]*Card(nil), w.s.Fleet.Cell(row, Ready)...)
		SortCards(ready)
		seats[i].Running = nil
		for _, c := range ready[:min(max(0, width-w.s.Fleet.Count(row, Working)), len(ready))] {
			seats[i].Running = append(seats[i].Running, c.ID)
		}
	}
	units, _ := friendStartUnits(w.s, seats)
	w.must(Plan{Units: units})
}

// dealStarted is the tick's deal part with these friends, applied, and then each friend up
// starts the cards her free lanes hold (startLanes): her width working and the rest ready.
func dealStarted(w *world, seats ...FriendSeat) Plan {
	w.t.Helper()
	p := dealWith(w, seats...)
	startLanes(w, slices.Clone(seats)...)
	return p
}
