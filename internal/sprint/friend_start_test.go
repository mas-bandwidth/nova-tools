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
// receipt (her beat naming it running) moves it to working and stamps its deadline then; a
// card dealt and not started within the start bound, while her beat names nothing running,
// is levelled to an eligible friend with an idle lane, never back to her.
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

	// dealt with three lanes free: three ready on her row, none working, none taken
	tick()
	require.Equal(t, 3, w.s.Fleet.Count(amy, Ready), "a friend's card is dealt ready, whatever her lanes")
	assert.Zero(t, w.s.Fleet.Count(amy, Working), "nothing she has not started is working")
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		wc := w.s.Fleet.Card(id)
		assert.Empty(t, wc.F("taken"), "%s: no deadline runs on a card she has not started", id)
		assert.Equal(t, Working, w.s.StateOf(wc.F("primary")), "its primary is dealt, as a machine's is")
	}

	// a tick later, with no start receipt, nothing moves
	w.tick(5 * time.Minute)
	tick()
	assert.Zero(t, w.s.Fleet.Count(amy, Working))

	// her beat names s1-1.w1 running: the next tick moves it to working, taken now
	w.tick(time.Minute)
	started := w.s.Now
	p := tick("s1-1.w1")
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, Working, wc.Col, "her start receipt moves it to working")
	assert.Equal(t, stamp(started), wc.F("taken"), "its deadline runs from her start")
	assert.Equal(t, stamp(started), wc.F("first_taken"))
	assert.Empty(t, wc.F("untaken_since"))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready))
	assert.Contains(t, movedLines(p), "s1-1.w1 friend.amy:ready -> working (started: her beat names it running)")

	// past the start bound while her beat still names a job running: she is working, and
	// her queue stays hers
	w.s.Now = t0.Add(FriendStartMaxDefault + time.Minute)
	tick("s1-1.w1")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "a friend running a job keeps her queue")

	// her beat names nothing running: her oldest unstarted card goes to bob, who has an idle
	// lane, ready on his row (not started there either), with one line; the other has no
	// friend with an idle lane to go to and stays
	p = tick()
	moved := w.s.Fleet.Card("s1-2.w1")
	assert.Equal(t, bob, moved.Row, "levelled to an eligible friend with an idle lane")
	assert.Equal(t, Ready, moved.Col, "ready on his row until he starts it")
	assert.Equal(t, "2", moved.F("gen"))
	assert.Contains(t, Split(moved.F(FieldFriendsLeft)), "amy", "never back to her")
	assert.Equal(t, amy, w.s.Fleet.Card("s1-3.w1").Row, "no other friend has an idle lane")
	var lines []string
	for _, l := range movedLines(p) {
		if strings.Contains(l, "not started") {
			lines = append(lines, l)
		}
	}
	require.Len(t, lines, 1, "one log line for the card levelled")
	assert.Contains(t, lines[0], "s1-2.w1 friend.amy:ready -> friend.bob:ready gen=2 (not started 20m0s after its deal, and friend amy names no job running)")

	// bob does not start it either, and amy, running a job, has an idle lane: it is not
	// levelled back to her
	w.tick(FriendStartMaxDefault + time.Minute)
	p = tick("s1-1.w1")
	assert.Equal(t, bob, w.s.Fleet.Card("s1-2.w1").Row, "a card never goes back to a friend it left")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, amy, w.s.Fleet.Card("s1-3.w1").Row, "a friend running a job keeps her queue")
	assert.Empty(t, Check(w.s, nil))

	// the start bound is a setting
	w.s.Work.SetProp(PropFriendStartMax, "45m")
	assert.Equal(t, 45*time.Minute, w.s.FriendStartMax())
	w.s.Work.SetProp(PropFriendStartMax, "nonsense")
	assert.Equal(t, FriendStartMaxDefault, w.s.FriendStartMax())
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
