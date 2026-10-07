package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The interim rules of 2026-10-06 (the owner, 7:25 PM ET: "fix it now, to work around it";
// docs/SPEC-SPRINT.md, the interim rules until read cards): the ask picks from the free
// readers themselves, a read retired without a verdict spends no reader, a pro card is read
// on flash while no enabled route serves pro, and the fleet's and the friends' work each
// have a switch. Every test is on the in-memory world, no store and no socket.

// spendSecond puts the reader's second identity read card of the primary at the attempt,
// retired with no verdict: the reader's one re-ask under the interim rule (ReadCardForAsk)
// is spent.
func spendSecond(w *world, primary string, attempt int, reader string) {
	pr := w.s.Work.Card(primary)
	w.s.Readers.Put(&Card{ID: ReadCardSecondID(primary, attempt, reader), Score: pr.Score, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": primary, "stream": pr.Row, "reader": reader, "attempt": itoa(attempt), "head": pr.F("head"),
		"retired": stamp(t0), "retired_by": "returned"}})
}

// A pro card is read on flash while no enabled route serves pro and one serves flash (the
// owner, 2026-10-06 7:11 PM ET: "let flash read pro"): its read card is drawn on a flash
// route and records tier flash. With a pro route enabled it is read on pro, and with no
// flash route either it stays pro.
func TestAProCardIsReadOnFlashWhenNoProRouteIsEnabled(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2")
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", Count: 1}))
	w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RoutePro
	toReview(w, "s1-1") // worked before the routes are read: the work's route is not the subject
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Tokens: 1000, Enabled: true},
		{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "q", Tokens: 1000, Enabled: false},
	}
	readersReadEveryTier(w)
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, cardhdr.RouteFlash, w.s.readTierOf(pr), "no pro route enabled: read on flash")
	w.must(CutReadCards(w.s, nil))
	reads := readCardsAt(w.s, pr, 1)
	require.NotEmpty(t, reads, "the pro card is asked of a reader")
	for _, rc := range reads {
		assert.Equal(t, "flash-a", rc.F(FieldRoute), rc.ID)
		assert.Equal(t, cardhdr.RouteFlash, rc.F(FieldTier), rc.ID)
	}
	w.s.Routes[1].Enabled = true
	assert.Equal(t, cardhdr.RoutePro, w.s.readTierOf(pr), "a pro route enabled: read on pro")
	w.s.Routes[0].Enabled, w.s.Routes[1].Enabled = false, false
	assert.Equal(t, cardhdr.RoutePro, w.s.readTierOf(pr), "no flash route either: pro, and its judgment")
}

// With the fleet's work off (nova-sprint set --fleet off) the tick deals no work card to a
// machine, the deal verb refuses, and the fleet's idle alarm is not raised; the readers
// still read. On again, the tick deals.
func TestFleetOffDealsNoWorkToMachines(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	toReview(w, "s1-1")
	w.s.Work.Put(&Card{ID: "s1-9", Row: "s1", Col: Waiting, Score: 9, Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "0", "stream": "s1", "brief": proBrief}})
	w.must(Set(w.s, SetReq{Fleet: SwitchOff, Who: "coordinator"}))
	require.True(t, w.s.FleetOff())
	dealt := func() int { return w.s.Fleet.Count("m1", Ready) + w.s.Fleet.Count("m2", Ready) }
	before := dealt()

	p, _ := TickDeal(w.s, TickReq{})
	w.must(p)
	assert.Equal(t, before, dealt(), "no work card is dealt to a machine")
	assert.Empty(t, w.notesOf(NNoMember))
	assert.Empty(t, w.notesOf(NStarving))
	refused := Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}})
	require.Len(t, refused.Refused, 1)
	assert.Contains(t, refused.Refused[0].Why, "--fleet on")
	p, _ = TickIdle(w.s, TickReq{IdleAlarm: true})
	assert.Empty(t, p.Units, "no idle alarm while the fleet's work is off")

	w.askReads()
	assert.NotEmpty(t, readCardsAt(w.s, w.s.Work.Card("s1-1"), 1), "reads flow")

	w.must(Set(w.s, SetReq{Fleet: SwitchOn, Who: "coordinator"}))
	require.False(t, w.s.FleetOff())
	p, _ = TickIdle(w.s, TickReq{IdleAlarm: true})
	assert.NotEmpty(t, p.Units, "on: the idle episode begins")
	p, _ = TickDeal(w.s, TickReq{})
	w.must(p)
	assert.Greater(t, dealt(), before, "on: the tick deals")
}

// With the friends' work off (nova-sprint set --friends off) the tick deals no work card to
// a friend, her reads are still placed on her row, and her empty row is no alarm. On again,
// she is dealt work in the room her reads leave.
func TestFriendsOffDealsNoWorkToFriends(t *testing.T) {
	t.Parallel()
	w, amy := priorityWorld(t, 1, 1, 0, 10)
	w.must(Set(w.s, SetReq{Friends: SwitchOff, Who: "coordinator"}))
	require.True(t, w.s.FriendsOff())
	tickDealAndAsk(t, w, amy)
	assert.Equal(t, []string{"s1-1"}, friendReads(w, "amy"), "her read flows")
	assert.Empty(t, friendNewWork(w, "amy"), "no work card is dealt to a friend")
	var empty Plan
	assert.Empty(t, emptyConds(&empty, w.s, TickReq{Friends: []FriendSeat{amy}}), "no empty-row alarm while the friends' work is off")

	w.must(Set(w.s, SetReq{Friends: SwitchOn, Who: "coordinator"}))
	tickDealAndAsk(t, w, amy)
	assert.Len(t, friendNewWork(w, "amy"), 2, "on: her room left is dealt")
}

// The switches take on and off, are the sprint's alone, and say off on the set line.
func TestTheWorkSwitchesAreTheSprints(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	p := Set(w.s, SetReq{Fleet: "maybe", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "--fleet wants on or off")
	p = Set(w.s, SetReq{Streams: []string{"s1"}, Friends: SwitchOff, Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "--friends is the sprint's")
	p = w.must(Set(w.s, SetReq{Fleet: SwitchOff, Friends: SwitchOff, Who: "coordinator"}))
	assert.Contains(t, p.Units[0].Moved, "fleet off")
	assert.Contains(t, p.Units[0].Moved, "friends off")
	assert.Equal(t, SwitchOff, SwitchWord(map[string]string{PropFleet: SwitchOff}, PropFleet))
	assert.Equal(t, SwitchOn, SwitchWord(nil, PropFriends), "on by default")
}

// A heavy card is read on pro (the owner, 2026-10-06 7:41 PM ET: "let pro do it"): the heavy
// friend is asked at the tier before the collapse, and a fleet pro reader is its second
// reader, on a pro route, in the same tick (reads are asked together). Before, a heavy read drew from heavy, which no route serves,
// and the fleet reader was asked nothing.
func TestAHeavyCardIsReadOnPro(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	w := newWorld(t, "reader-m1", "reader-m2")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	johnny := FriendSeat{Name: "johnny", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteHeavy}, Roles: []string{RoleReader}}
	w.s.Friends = []FriendSeat{johnny}
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Tokens: 1000, Enabled: true},
		{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "q", Tokens: 1000, Enabled: true},
	}
	// one fleet reader of pro up (reader-m2 reads flash alone): with the friend's read it
	// is the two readers a heavy card needs (enoughReadersUp counts her read)
	w.s.Readers.Texts = map[string]map[string]string{"reader-m1": {ReaderTiers: "flash,pro"}, "reader-m2": {ReaderTiers: "flash"}}
	w.s.ReaderStates = map[string]string{"reader-m1": ReaderUp, "reader-m2": ReaderUp}
	putReview(w, "s1-1", "s1-1: heavy work tier: heavy\n\nThe task.", 1, 1, head)
	w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RouteHeavy
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, cardhdr.RoutePro, w.s.readTierOf(pr), "a heavy read collapses to pro")
	require.Equal(t, 2, ReadsNeeded(pr), "a heavy card needs two readers")

	tickDealAndAsk(t, w, johnny)
	require.NotNil(t, w.s.Fleet.Placed(ReadCardID("s1-1", 1, "johnny")), "the heavy friend is asked, at the tier before the collapse")
	rc := w.s.Fleet.Placed(ReadCardID("s1-1", 1, "m1"))
	require.NotNil(t, rc, "in the same deal (reads together) the second read card is the fleet pro reader's")
	assert.Nil(t, w.s.Fleet.Placed(ReadCardID("s1-1", 1, "m2")), "m2 reads flash alone")
	assert.Equal(t, "pro-a", rc.F(FieldRoute))
	assert.Equal(t, cardhdr.RoutePro, rc.F(FieldTier))
}
