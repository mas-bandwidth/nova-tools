package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A heavy read is asked of a friend reader of the class before any route is looked
// for (docs/SPEC-SPRINT.md section 6, the order of a read; 2026-10-06 12:05 ET, seven
// heavy primaries held by "no route serves the tier" while the friend rows carried
// flash,frontier,heavy,pro and reader-rowan-space, reader-rowan-space-2 and
// reader-stella were up; the owner, 12:22 ET: "All rowans can take the set of
// Fable/Opus/Sonnet/Haiku"). A friend reader's class is her friend row's, never her
// readers-table cell and never a route: here the cells say flash,pro and the rows say
// heavy. The read is asked of the friend on another login than the attempt's worker
// first, then of the worker's own friend reader, and of the fleet reader on the heavy
// route only when no friend reader of the class is free. The tier's no-route judgment
// is raised only when no reader of the class is up either.
func TestAHeavyReadIsAskedOfAFriendReaderOfTheClassBeforeARoute(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	class := "flash,frontier,heavy,pro"
	setup := func(heavyRoute bool, up ...string) *world {
		w := newWorld(t, "reader-m1", "reader-rowan", "reader-stella")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
		w.s.Friends = []FriendSeat{
			{Name: "rowan", Width: 1, Status: Up, Class: class},
			{Name: "rowan-personal", Width: 1, Status: Up, Class: class},
			{Name: "stella", Width: 1, Status: Up, Class: class},
		}
		// the readers table's cells are narrower than the friend rows: the row decides
		w.s.Readers.Texts = map[string]map[string]string{
			"reader-rowan": {ReaderTiers: "flash,pro"}, "reader-stella": {ReaderTiers: "flash,pro"},
		}
		w.s.Routes = []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
			{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "q", Enabled: true},
		}
		if heavyRoute {
			w.s.Routes = append(w.s.Routes, Route{Name: "heavy-a", Tier: cardhdr.RouteHeavy, Provider: "p", Model: "h", Enabled: true})
		}
		putReview(w, "s1-1", "s1-1: heavy work tier: heavy\n\nThe task.", 1, 1, head)
		pr := w.s.Work.Card("s1-1")
		pr.Fields[FieldTierNow] = cardhdr.RouteHeavy
		pr.Fields["member"] = FriendRow("rowan") // rowan worked the attempt
		w.s.ReaderStates = map[string]string{"reader-m1": ReaderUp, "reader-rowan": ReaderDown, "reader-stella": ReaderDown}
		for _, rd := range up {
			w.s.ReaderStates[rd] = ReaderUp
		}
		require.Equal(t, cardhdr.RouteHeavy, w.s.readTierOf(pr))
		return w
	}
	held := func(w *world) bool {
		for _, n := range w.notesOf(NNoRoute) {
			if n.Stream == TierSubject(cardhdr.RouteHeavy) && contains(n.Primaries, "s1-1") {
				return true
			}
		}
		return false
	}
	tick := func(w *world) {
		w.t.Helper()
		p, _ := TickAsk(w.s, TickReq{})
		w.must(p)
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
	}
	readOk := func(w *world, rc *Card) {
		w.t.Helper()
		w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}},
			Usage: "model=anthropic/claude-opus-5-5 wall=300s harness=claude"}))
	}

	// a heavy route is there to draw, and the fleet reader could run it: the friends
	// are asked first all the same, stella (another login) before rowan (the worker's)
	w := setup(true, "reader-rowan", "reader-stella")
	tick(w)
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 1, "the first read is asked, one at a time")
	assert.Equal(t, "reader-stella", reads[0].F("reader"), "a friend reader of the class on another login than the worker, before the route")
	assert.Equal(t, cardhdr.RouteHeavy, reads[0].F(FieldTier))
	assert.False(t, held(w))
	readOk(w, reads[0])
	tick(w)
	reads = readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2)
	assert.ElementsMatch(t, []string{"reader-stella", "reader-rowan"}, readerNames(reads), "the worker's own friend reader before the fleet reader on the route")

	// no heavy route at all: the friend readers of the class read it, and the tier's
	// judgment is not raised (the defect: their cells said flash,pro, so it was). The
	// pair needs both of them here, the worker's own too, so the room orders the two.
	n := setup(false, "reader-rowan", "reader-stella")
	tick(n)
	reads = readsAt(n.s, n.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 1)
	assert.Contains(t, []string{"reader-rowan", "reader-stella"}, reads[0].F("reader"))
	assert.Equal(t, cardhdr.RouteHeavy, reads[0].F(FieldTier))
	assert.Equal(t, "", reads[0].F(FieldRoute), "no heavy route to draw")
	assert.False(t, held(n), "a friend reader of the class is up: no no-route judgment")
	assert.Empty(t, n.notesOf(NCannotAsk))
	readOk(n, reads[0])
	tick(n)
	reads = readsAt(n.s, n.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2)
	assert.ElementsMatch(t, []string{"reader-stella", "reader-rowan"}, readerNames(reads))
	assert.False(t, held(n))

	// only stella up: the route's fleet reader is asked the second read, on the route
	r := setup(true, "reader-stella")
	tick(r)
	reads = readsAt(r.s, r.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 1)
	assert.Equal(t, "reader-stella", reads[0].F("reader"))
	readOk(r, reads[0])
	tick(r)
	reads = readsAt(r.s, r.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2)
	var fleet *Card
	for _, rc := range reads {
		if rc.F("reader") == "reader-m1" {
			fleet = rc
		}
	}
	require.NotNil(t, fleet, "no friend reader of the class free: then the route")
	assert.Equal(t, "heavy-a", fleet.F(FieldRoute))

	// the control: no friend reader of the class up and no heavy route: the judgment
	c := setup(false)
	tick(c)
	assert.Empty(t, readsAt(c.s, c.s.Work.Card("s1-1"), 1))
	assert.True(t, held(c), "no reader of the class and no route serve heavy: the tier's judgment")
}
