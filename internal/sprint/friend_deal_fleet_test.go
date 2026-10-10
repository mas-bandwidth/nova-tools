package sprint

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// A friend with room is dealt the fleet's cards of her tier (docs/SPEC-SPRINT.md section 1,
// a friend's card; the owner, 2026-10-06 4:30 PM ET). That afternoon a
// friend up, tiers flash, width 32, holding 13 reads, had nothing ready for over an hour
// while the fleet was dealt flash cards: the friend deal read an undealt card's tier as its
// ceiling (a brief whose line 1 says tier: heavy), while the machines deal every such card
// on flash first (startTier), and her reads counted as her lanes.

// fleetBrief is a fleet card's brief with line 1 naming the tier given ("" names none).
func fleetBrief(tier string) string {
	first := "c: a fleet card"
	if tier != "" {
		first += " tier: " + tier
	}
	return first + "\nREPO: mas-bandwidth/nova-tools\n\nThe task."
}

// putReads places n read cards working on the friend's row, as the ask places hers, each
// of a primary in review in stream reads.
func putReads(w *world, friend string, n int) {
	row := FriendRow(friend)
	if !w.s.Fleet.HasRow(row) {
		w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
	}
	if !w.s.Work.HasRow("reads") {
		w.s.Work.SetRows(append(w.s.Work.Rows(), "reads"))
	}
	for i := range n {
		w.s.Work.Put(&Card{ID: "read-" + itoa(i), Row: "reads", Col: Review, Score: float64(i + 1), Rev: 1, Fields: map[string]string{
			"kind": "primary", "attempt": "1", "stream": "reads", "brief": fleetBrief("flash"), "head": "h"}})
		w.s.Fleet.Put(&Card{ID: ReadCardID("read-"+itoa(i), 1, friend), Row: row, Col: Working, Score: float64(i + 1), Rev: 1, Fields: map[string]string{
			"kind": "read", "primary": "read-" + itoa(i), "stream": "reads", "reader": friend, "attempt": "1"}})
	}
}

// aheadWorld is two machines up at width 2, each dealt ahead its room of flash cards
// (stream ahead, ready on its row and untaken), and the cards of each stream given added.
func aheadWorld(t *testing.T, streams map[string][]string) *world {
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 2}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 2}))
	var ahead []CardAdd
	for i := range 2 * DealAhead * 2 {
		ahead = append(ahead, CardAdd{ID: "ahead-" + itoa(i+1), Brief: fleetBrief("flash")})
	}
	w.must(Add(w.s, AddReq{Stream: "ahead", Cards: ahead}))
	dealWith(w)
	for _, m := range []string{"m1", "m2"} {
		require.Equal(t, DealAhead*2, w.s.Fleet.Count(m, Ready), "%s is dealt ahead its room, untaken", m)
	}
	for _, st := range []string{"fleet", "who"} {
		var cards []CardAdd
		for i, b := range streams[st] {
			cards = append(cards, CardAdd{ID: st + "-" + itoa(i+1), Brief: b})
		}
		if len(cards) > 0 {
			w.must(Add(w.s, AddReq{Stream: st, Cards: cards}))
		}
	}
	return w
}

// workOn is the work cards on the row, ready and working.
func workOn(w *world, row string) int {
	n := 0
	for _, c := range append(slices.Clone(w.s.Fleet.Cell(row, Ready)), w.s.Fleet.Cell(row, Working)...) {
		if c.F("kind") == "work" {
			n++
		}
	}
	return n
}

func TestAFlashFriendWithRoomIsDealtFleetFlashCards(t *testing.T) {
	t.Parallel()
	var fleet []string
	for range 8 {
		fleet = append(fleet, fleetBrief("flash"))
	}
	for range 6 {
		fleet = append(fleet, fleetBrief(""))
	}
	for range 6 {
		fleet = append(fleet, fleetBrief("heavy")) // its ceiling: the machines deal it on flash first
	}
	proBob := "c: a friend's card tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: friend bob\n\nThe task."
	who := []string{proBob, proBob, proBob}
	w := aheadWorld(t, map[string][]string{"fleet": fleet, "who": who})
	putReads(w, "freddy", 13)
	freddy := FriendSeat{Name: "freddy", Width: 32, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	bob := FriendSeat{Name: "bob", Width: 4, Status: Up, Class: "pro", Tiers: []string{"pro"}}
	dealWith(w, freddy, bob)
	for i := range fleet {
		id := "fleet-" + itoa(i+1)
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		require.NotNil(t, wc, "%s is dealt", id)
		assert.Equal(t, FriendRow("freddy"), wc.Row, "%s goes to the flash friend, within her room (her 13 reads and her work in one width)", id)
		assert.Equal(t, "flash", w.s.Work.Card(id).F(FieldTierNow), "%s is dealt on flash, as the machines deal it", id)
	}
	for i := range who {
		id := "who-" + itoa(i+1)
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		require.NotNil(t, wc, id)
		assert.Equal(t, FriendRow("bob"), wc.Row, "%s goes to the friend it names", id)
	}
	assert.Equal(t, 13, w.s.Fleet.Count(FriendRow("freddy"), Working), "her reads are where they were")
	dealtFleet := FriendsDealtFleet(w.s)
	assert.GreaterOrEqual(t, dealtFleet["freddy"], len(fleet), "where's dealt_fleet counts the fleet's cards she holds")
	assert.Zero(t, dealtFleet["bob"], "a card whose WHO line names her is no fleet card")
	assert.Empty(t, Check(w.s, nil))
}

// One width bounds a friend's row, her reads and her work together (the owner's rule): a
// friend at width 16 holding 10 work cards and 9 reads has no lane free and is dealt no
// work, and a one-shot friend holding a read is dealt no work card.
func TestOneWidthHoldsHerWorkAndReads(t *testing.T) {
	t.Parallel()
	briefs := make([]string, 30)
	for i := range briefs {
		briefs[i] = fleetBrief("flash")
	}
	w := friendWorld(t, briefs...)
	amy := FriendSeat{Name: "amy", Width: 16, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	row := FriendRow("amy")
	w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
	for i := range 10 {
		w.s.Fleet.Put(&Card{ID: WorkCardID("busy-"+itoa(i), 1), Row: row, Col: Working, Rev: 1, Fields: map[string]string{
			"kind": "work", "primary": "busy-" + itoa(i), "stream": "s9", "attempt": "1"}})
	}
	putReads(w, "amy", 9)
	assert.Equal(t, 19, friendLoad(w.s, "amy"), "her load is her work and her reads")
	room, width := friendRoom(amy)
	assert.Equal(t, 13, room-friendLoad(w.s, "amy"), "her room less work and reads")
	assert.Equal(t, -3, width-friendLoad(w.s, "amy"), "her lanes: 16 less 19, none idle")

	// the one-shot friend: a read on her row is her one card
	w2 := friendWorld(t, fleetBrief("flash"))
	putReads(w2, "bee", 1)
	dealWith(w2, FriendSeat{Name: "bee", Width: 4, Status: Up, Mode: config.FriendModeOneShot, Class: "flash", Tiers: []string{"flash"}})
	assert.Zero(t, workOn(w2, FriendRow("bee")), "one card at a time, read or work")
	wc := w2.s.Fleet.Card(WorkCardID("s1-1", 1))
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row, "the machines take it")

	// her read room is her room: with her row full of work she is asked no read
	w3 := newWorld(t, "reader-a", "reader-b")
	putReview(w3, "s1-1", "s1-1: read this (s1) tier: flash\n", 1, 1, "head-1")
	putAttemptWork(w3, "s1-1", 1, "sprint/s1-1", "cccccccccccccccccccccccccccccccccccccccc", "")
	bee := FriendSeat{Name: "bee", Width: 1, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	w3.s.Fleet.SetRows(append(w3.s.Fleet.Rows(), FriendRow("bee")))
	for i := range DealAhead {
		w3.s.Fleet.Put(&Card{ID: WorkCardID("busy-"+itoa(i), 1), Row: FriendRow("bee"), Col: Working, Rev: 1, Fields: map[string]string{
			"kind": "work", "primary": "busy-" + itoa(i), "stream": "s9", "attempt": "1"}})
	}
	p, _, err := friendReadAsk(w3.s, []FriendSeat{bee}, "")
	require.NoError(t, err)
	w3.do(p)
	assert.Nil(t, w3.s.Fleet.Card(ReadCardID("s1-1", 1, "bee")), "her row is full: one width, no read room beside it")
}

func TestAnUntieredCardIsFlashForAFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, fleetBrief(""), fleetBrief("heavy"), fleetBrief("pro"))
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, "flash", w.s.dealTierOf(pr), "no tier line: flash, as the machines deal it")
	assert.Equal(t, "flash", w.s.dealTierOf(w.s.Work.Card("s1-2")), "a heavy ceiling starts on flash")
	assert.Equal(t, "pro", w.s.dealTierOf(w.s.Work.Card("s1-3")), "a pro brief starts on pro")
	amy := FriendSeat{Name: "amy", Width: 4, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	dealWith(w, amy)
	for _, id := range []string{"s1-1", "s1-2"} {
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		require.NotNil(t, wc, id)
		assert.Equal(t, FriendRow("amy"), wc.Row, "%s is a flash friend's", id)
	}
	wc := w.s.Fleet.Card(WorkCardID("s1-3", 1))
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row, "a pro card is no flash friend's")
}

func TestAFriendReclaimsAnUntakenDealtAheadCard(t *testing.T) {
	t.Parallel()
	w := aheadWorld(t, nil)
	// one of m1's cards is taken: it is the machine's
	taken := w.s.Fleet.Cell("m1", Ready)[0]
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{taken.ID}}, Gens: gensOf(w.s, taken.ID)}))
	require.Equal(t, Working, w.s.Fleet.Card(taken.ID).Col)
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	cat := FriendSeat{Name: "cat", Width: 2, Status: Up, Class: "pro", Tiers: []string{"pro"}}
	p := dealWith(w, amy, cat)
	assert.Equal(t, 2, workOn(w, FriendRow("amy")), "her idle lanes take untaken dealt-ahead cards, no more")
	assert.Zero(t, workOn(w, FriendRow("cat")), "a friend whose tiers do not hold flash reclaims none")
	assert.Equal(t, "m1", w.s.Fleet.Card(taken.ID).Row, "a taken card stays the machine's")
	n := 0
	for _, c := range w.s.Fleet.Cell(FriendRow("amy"), Ready) {
		assert.Empty(t, c.F(FieldRoute), "a friend runs her own model: the fleet route comes off")
		assert.Equal(t, "2", c.F("gen"), "its next generation: the machine's take of the old one is refused")
		assert.Equal(t, FriendRow("amy"), c.F("member"))
		assert.Equal(t, c.ID, w.s.Work.Card(c.F("primary")).F("work"), "its primary stays working on it")
		n++
	}
	assert.Equal(t, 2, n, "ready on her row until she starts it")
	assert.Equal(t, map[string]int{"amy": 2}, FriendsDealtFleet(w.s), "where's dealt_fleet")
	moved := 0
	for _, u := range p.Units {
		if strings.Contains(u.Moved, "reclaimed") {
			moved++
		}
	}
	assert.Equal(t, 2, moved, "each reclaim says so")
	assert.Empty(t, Check(w.s, nil))
}

// The reclaim is bounded each tick: a friend takes at most her idle lanes, a machine gives at
// most half its dealt-ahead queue, the machines in turn; a reclaimed card she does not start
// and that is taken back goes to the machines' deal again, never to her.
func TestTheReclaimIsBoundedAndAnUnstartedReclaimGoesBackToTheMachines(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 4}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2", Width: 4}))
	var ahead []CardAdd
	for i := range 16 {
		ahead = append(ahead, CardAdd{ID: "ahead-" + itoa(i+1), Brief: fleetBrief("flash")})
	}
	w.must(Add(w.s, AddReq{Stream: "ahead", Cards: ahead}))
	dealWith(w)
	require.Equal(t, 8, w.s.Fleet.Count("m1", Ready))
	require.Equal(t, 8, w.s.Fleet.Count("m2", Ready))

	// three idle lanes: three cards, the machines in turn
	amy := FriendSeat{Name: "amy", Width: 3, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	dealWith(w, amy)
	assert.Equal(t, 3, workOn(w, FriendRow("amy")), "at most her idle lanes")
	assert.Equal(t, 6, w.s.Fleet.Count("m1", Ready), "m1 gave two, its turn first and third")
	assert.Equal(t, 7, w.s.Fleet.Count("m2", Ready), "m2 gave one")

	// a friend as wide as the fleet takes at most half of each machine's queue in a tick
	w2 := newWorld(t, "reader-a", "reader-b")
	w2.must(FleetStep(w2.s, FleetReq{Op: "up", Member: "m1", Width: 4}))
	w2.must(FleetStep(w2.s, FleetReq{Op: "up", Member: "m2", Width: 4}))
	w2.must(Add(w2.s, AddReq{Stream: "ahead", Cards: ahead}))
	dealWith(w2)
	wide := FriendSeat{Name: "fay", Width: 32, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	dealWith(w2, wide)
	assert.Equal(t, 8, workOn(w2, FriendRow("fay")), "half of each machine's eight")
	assert.Equal(t, 4, w2.s.Fleet.Count("m1", Ready))
	assert.Equal(t, 4, w2.s.Fleet.Count("m2", Ready))

	// one of amy's reclaimed cards she never starts is taken back: the machines deal it again
	wc := w.s.Fleet.Cell(FriendRow("amy"), Ready)[0]
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{wc.ID}, Reason: "not started past its bound", Who: "coordinator"}))
	require.Equal(t, Withdrawn, w.s.Fleet.Card(wc.ID).Col)
	dealWith(w, amy)
	back := w.s.Fleet.Card(wc.ID)
	assert.Contains(t, []string{"m1", "m2"}, back.Row, "dealt again to a machine, never back to her")
}

// A card dealt again to a friend whose primary names no tier_now gets the tier the deal drew
// (a heavy ceiling, dealt on flash), so its reads follow the deal tier, not the ceiling.
func TestARedealToAFriendWritesItsTierNow(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, fleetBrief("heavy"))
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	dealWith(w, bob)
	wc := w.s.Fleet.Card(WorkCardID("s1-1", 1))
	require.NotNil(t, wc)
	require.Equal(t, FriendRow("bob"), wc.Row)
	pr := w.s.Work.Card("s1-1")
	delete(pr.Fields, FieldTierNow) // a card dealt before the deal wrote it
	w.s.Work.Put(pr)
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "bob", IDs: []string{wc.ID}, Reason: "taken back", Who: "coordinator"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	dealWith(w, amy)
	require.Equal(t, FriendRow("amy"), w.s.Fleet.Card(wc.ID).Row)
	now, ceiling := CardTiers(w.s.Work.Card("s1-1"))
	assert.Equal(t, "flash", now, "the tier the deal drew")
	assert.Equal(t, "heavy", ceiling)
}

// where's dealt_fleet counts the work cards on her row whose primary carries no WHO line: a
// bare WHO: friend card, like a named one, is a friend's card.
func TestDealtFleetCountsOnlyCardsWithNoWhoLine(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, fleetBrief("flash"), friendBrief("friend"), friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 4, Status: Up, Class: "flash", Tiers: []string{"flash"}}
	dealWith(w, amy)
	require.Equal(t, 3, workOn(w, FriendRow("amy")))
	assert.Equal(t, map[string]int{"amy": 1}, FriendsDealtFleet(w.s), "one card of the fleet's; WHO: friend and WHO: friend amy are hers")
}
