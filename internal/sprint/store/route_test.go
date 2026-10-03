package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The deal's route (internal/sprint/route.go; tla/DirtyTick.tla, Deal's route
// guard) on the mem twin: the card's tier resolved through the store's routes at
// deal time, written on the work card and handed in its packet.

// routeSeconds is a route's deadline: seconds, as nova-config's route row holds it.
var routeSeconds = 600

func route(name, tier string) sprint.Route {
	return sprint.Route{Name: name, Tier: tier, Provider: "prov-" + name, Model: "model-" + name, Tokens: 1000, Deadline: routeSeconds, Enabled: true}
}

// briefOf is a brief whose line 1 names the tier, and whose header carries extra.
func briefOf(tier, extra string) string {
	return "c: the work (s1) tier: " + tier + "\n" + extra + "\nThe task.\n"
}

// routeHarness is the harness with its two members up and the store's routes.
func routeHarness(t *testing.T, routes ...sprint.Route) *harness {
	h := newHarness(t)
	// wide members: a test deals hundreds of cards, and the deal holds a member to its width
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: sprint.MaxWidth}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: sprint.MaxWidth}))
	h.m.SetRoutes(routes)
	return h
}

// addReady admits n cards of the brief to the stream, ready.
func (h *harness) addReady(stream string, n int, brief string) {
	h.t.Helper()
	h.must(AddStep(sprint.AddReq{Stream: stream, Count: n, Brief: brief}))
}

// workCards is the fleet table's work cards, by id.
func (h *harness) workCards() map[string]*sprint.Card {
	out := map[string]*sprint.Card{}
	for _, c := range h.snap().Fleet.Column(sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed, sprint.Withdrawn) {
		out[c.ID] = c
	}
	return out
}

func TestTheDealDrawsARouteOfTheCardsTierAndThePacketCarriesIt(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"), route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.must(DealStep(sprint.DealReq{}))
	wc := h.workCards()["s1-1.w1"]
	require.NotNil(t, wc)
	r := wc.F(sprint.FieldRoute)
	require.Contains(t, []string{"pro-a", "pro-b"}, r, "a pro card draws a pro route")
	assert.Equal(t, "prov-"+r+"/model-"+r, wc.F(sprint.FieldModel))
	assert.Equal(t, "1000", wc.F(sprint.FieldTokens))
	assert.Equal(t, "600", wc.F(sprint.FieldDeadline))
	assert.Equal(t, r, h.snap().Work.Card("s1-1").F(sprint.FieldRoutes), "the primary keeps the routes drawn for it")
	ps, err := h.st.Packets(h.ctx, []*sprint.Card{wc})
	require.NoError(t, err)
	assert.Equal(t, r, ps[0].Route)
	assert.Equal(t, "prov-"+r+"/model-"+r, ps[0].Model)
	assert.Equal(t, "1000", ps[0].Tokens)
	assert.Equal(t, 600, ps[0].Deadline)
}

// A route's dollar budget rides the work card and its packet beside the token budget, so the
// member launches native with --usd (nova-tools #5094); a route with none writes none, and
// the field is written empty so a redeal onto such a route clears what an earlier draw set.
func TestARoutesDollarBudgetRidesTheCardAndThePacket(t *testing.T) {
	t.Parallel()
	priced := route("pro-a", "pro")
	priced.USD = "0.5"
	h := routeHarness(t, priced, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.addReady("s2", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	wc := h.workCards()
	require.NotNil(t, wc["s1-1.w1"])
	require.NotNil(t, wc["s2-1.w1"])
	assert.Equal(t, "0.5", wc["s1-1.w1"].F(sprint.FieldUSD), "the route's dollar budget is on the work card")
	assert.Empty(t, wc["s2-1.w1"].F(sprint.FieldUSD), "a route with no dollar budget writes none")
	ps, err := h.st.Packets(h.ctx, []*sprint.Card{wc["s1-1.w1"], wc["s2-1.w1"]})
	require.NoError(t, err)
	assert.Equal(t, "0.5", ps[0].USD, "the packet hands the member the dollar budget")
	assert.Empty(t, ps[1].USD)
	// an empty dollar budget is no cap: the packet the member reads carries no usd at all,
	// so the member puts no --usd on the launch (member.go; TestAMemberLaunchesEachCardOnItsPacketsRoute)
	raw, err := json.Marshal(ps[1])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"usd"`, "an empty dollar budget is no word in the packet: %s", raw)
	raw, err = json.Marshal(ps[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"usd":"0.5"`)
	assert.Equal(t, "0.5", RouteOf("r", map[string]string{"usd": "0.5", "tier": "pro"}).USD, "the route's hash carries it")
}

func TestAPinnedCardRunsOnItsPinAndBypassesTheDraw(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("pro", "model: anthropic/claude-x\ntokens: 5000\ndeadline: 5m"))
	h.must(DealStep(sprint.DealReq{}))
	wc := h.workCards()["s1-1.w1"]
	assert.Equal(t, sprint.RoutePin, wc.F(sprint.FieldRoute))
	assert.Equal(t, "anthropic/claude-x", wc.F(sprint.FieldModel))
	assert.Equal(t, "5000", wc.F(sprint.FieldTokens))
	assert.Equal(t, "300", wc.F(sprint.FieldDeadline))
}

// A store with no route deals as before: no route on the card, and the member
// runs its own override.
func TestAStoreWithNoRouteDealsAsBefore(t *testing.T) {
	t.Parallel()
	h := routeHarness(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.must(DealStep(sprint.DealReq{}))
	wc := h.workCards()["s1-1.w1"]
	require.NotNil(t, wc)
	assert.Empty(t, wc.F(sprint.FieldRoute))
	assert.Empty(t, wc.F(sprint.FieldModel))
}

// A card whose tier no enabled route serves is not dealt: the deal verb refuses it
// naming the tier, and the tick writes one judgment for the tier however many cards
// of it wait, never one per card and never again while it is open; a route added
// for the tier deals them, and the judgment closes. A frontier card with no pin is
// the coordinator's, judged the same way, and a pinned one is dealt.
func TestACardWithNoRouteIsNotDealtAndJudgedOncePerTier(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), func() sprint.Route { r := route("pro-off", "pro"); r.Enabled = false; return r }())
	h.addReady("s1", 3, briefOf("pro", ""))
	h.addReady("s2", 1, briefOf("flash", ""))
	h.addReady("s3", 1, briefOf("frontier", ""))
	h.addReady("s4", 1, briefOf("frontier", "model: anthropic/claude-frontier\ntokens: unmetered\ndeadline: 3600"))
	res := h.run(DealStep(sprint.DealReq{}))
	refused := fmt.Sprint(res.Refused)
	assert.Contains(t, refused, "no enabled route serves tier pro")
	assert.Contains(t, refused, "a frontier card waits for the coordinator")
	cards := h.workCards()
	assert.Contains(t, cards, "s2-1.w1", "the flash card is dealt")
	assert.Contains(t, cards, "s4-1.w1", "the pinned frontier card is dealt")
	assert.NotContains(t, cards, "s1-1.w1")
	assert.NotContains(t, cards, "s3-1.w1")

	h.startMachine()
	for i := 0; i < 3; i++ {
		h.machine()
	}
	open := h.a2Open(sprint.NNoRoute)
	var subjects []string
	for _, o := range open {
		subjects = append(subjects, o.Subject())
	}
	assert.ElementsMatch(t, []string{sprint.StreamSubject(sprint.TierSubject("pro")), sprint.StreamSubject(sprint.TierSubject("frontier"))}, subjects,
		"one judgment per tier, however many cards of it wait")
	assert.Equal(t, 2, h.notesOf(sprint.NNoRoute), "written once each, never every tick")
	h.clean("the cards no route serves are held by their tier's judgment")

	h.m.SetRoutes([]sprint.Route{route("flash-a", "flash"), route("pro-a", "pro")})
	h.machine()
	cards = h.workCards()
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		require.Contains(t, cards, id, "a route for the tier deals its cards")
		assert.Equal(t, "pro-a", cards[id].F(sprint.FieldRoute))
	}
	open = h.a2Open(sprint.NNoRoute)
	require.Len(t, open, 1, "the pro judgment closes; the frontier one stays")
	assert.Equal(t, sprint.StreamSubject(sprint.TierSubject("frontier")), open[0].Subject())
}

// A work card withdrawn by its member going down is dealt again leaving out the
// route it was dealt on while another remains; with one route, the same one.
func TestARedealLeavesOutTheRouteItWasDealtOn(t *testing.T) {
	t.Parallel()
	for _, two := range []bool{true, false} {
		rs := []sprint.Route{route("pro-a", "pro")}
		if two {
			rs = append(rs, route("pro-b", "pro"))
		}
		h := newHarness(t)
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
		h.m.SetRoutes(rs)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.must(DealStep(sprint.DealReq{}))
		w1 := h.workCards()["s1-1.w1"]
		require.NotNil(t, w1)
		first := w1.F(sprint.FieldRoute)
		gens := map[string]int{w1.ID: w1.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: gens, Who: "m1"}))
		h.run(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
		h.run(DealStep(sprint.DealReq{}))
		wc := h.workCards()["s1-1.w1"]
		require.NotNil(t, wc)
		require.Equal(t, "m2", wc.Row, "dealt again to the member up")
		if two {
			assert.NotEqual(t, first, wc.F(sprint.FieldRoute), "the redeal leaves out %s", first)
		} else {
			assert.Equal(t, first, wc.F(sprint.FieldRoute), "one route: the redeal runs on it again")
		}
	}
}

// Three routes, three attempts each failed: every attempt leaves out
// every route drawn before, so the three attempts run on the three routes.
func TestTheExclusionCoversEveryAttempt(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"), route("pro-c", "pro"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.must(DealStep(sprint.DealReq{}))
	var seen []string
	for a := 1; a <= 3; a++ {
		wc := h.workCards()[fmt.Sprintf("s1-1.w%d", a)]
		require.NotNil(t, wc, "attempt %d", a)
		seen = append(seen, wc.F(sprint.FieldRoute))
		if a == 3 {
			break
		}
		gens := map[string]int{wc.ID: wc.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
		h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
			Report: "the model gave up", Who: wc.Row}))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Who: "tester"}))
	}
	assert.ElementsMatch(t, []string{"pro-a", "pro-b", "pro-c"}, seen)
}

// A card admitted before the lint whose model lines the deal cannot read (a pin
// with no budget, an unknown tier) is not dealt, and is judged once under the tier
// its line 1 names: never skipped in silence.
func TestACardWhoseModelLinesCannotBeReadIsJudgedUnderItsTier(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("pro", "model: x/y"))
	h.addReady("s2", 2, briefOf("medium", ""))
	res := h.run(DealStep(sprint.DealReq{}))
	assert.Contains(t, fmt.Sprint(res.Refused), "model: x/y pins the card without tokens: <n>|unmetered and deadline: <seconds>")
	assert.Contains(t, fmt.Sprint(res.Refused), "line 1 names tier medium")
	assert.Empty(t, h.workCards(), "neither is dealt")
	h.startMachine()
	h.machine()
	h.machine()
	var subjects []string
	for _, o := range h.a2Open(sprint.NNoRoute) {
		subjects = append(subjects, o.Subject())
	}
	assert.ElementsMatch(t, []string{sprint.StreamSubject(sprint.TierSubject("pro")), sprint.StreamSubject(sprint.TierSubject("medium"))}, subjects)
	h.clean("held by their tier's judgment")
}

// A route disabled by an apply is out of the draw from the next tick.
func TestADisabledRouteIsOutOfTheNextTicksDraw(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"))
	h.startMachine()
	off := route("pro-a", "pro")
	off.Enabled = false
	h.m.SetRoutes([]sprint.Route{off, route("pro-b", "pro")})
	h.addReady("s1", 40, briefOf("pro", ""))
	h.machine()
	cards := h.workCards()
	require.NotEmpty(t, cards)
	for _, wc := range cards {
		assert.Equal(t, "pro-b", wc.F(sprint.FieldRoute), wc.ID)
	}
}

// A tick reads the routes once, shared by the deal and the check: the deal and
// the check of one tick plan on the same read (a Mem store makes no round trips;
// the count is the drive's, cmd/nova-sprint's dirty-tick drive).
func TestATickReadsTheRoutesOnce(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"))
	reads := 0
	h.m.Fail = func(point string) error {
		if point == "routes" {
			reads++
		}
		return nil
	}
	h.addReady("s1", 3, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	assert.Equal(t, 1, reads, "one read of the routes in the tick")
}

// A later attempt draws again leaving out the routes already drawn for the card
// while another remains; with one route, it runs on that one as before. The
// attempt's record (card <id>) names the route, the model, the usage and the end.
func TestALaterAttemptLeavesOutTheRoutesAlreadyDrawn(t *testing.T) {
	t.Parallel()
	for _, routes := range [][]sprint.Route{
		{route("pro-a", "pro"), route("pro-b", "pro")},
		{route("pro-a", "pro")},
	} {
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.must(DealStep(sprint.DealReq{}))
		w1 := h.workCards()["s1-1.w1"]
		first := w1.F(sprint.FieldRoute)
		gens := map[string]int{w1.ID: w1.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: w1.Row, Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: gens, Who: w1.Row}))
		h.must(FinishStep(sprint.FinishReq{As: w1.Row, Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: gens, Failed: true,
			Report: "the model gave up", Usage: "wall=12.00s budget=300/1000", Who: w1.Row}))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Who: "tester"}))
		cards := h.workCards()
		w2 := cards["s1-1.w2"]
		require.NotNil(t, w2)
		if len(routes) == 2 {
			assert.NotEqual(t, first, w2.F(sprint.FieldRoute), "attempt 2 leaves out the route attempt 1 drew")
		} else {
			assert.Equal(t, first, w2.F(sprint.FieldRoute), "one route: attempt 2 runs on it again")
		}
		assert.Equal(t, first+","+w2.F(sprint.FieldRoute), h.snap().Work.Card("s1-1").F(sprint.FieldRoutes))

		line := sprint.AttemptLine(cards["s1-1.w1"])
		for _, want := range []string{"ATTEMPT 1 ", "route=" + first, "model=prov-" + first + "/model-" + first, "member=" + w1.Row,
			"usage=wall=12.00s budget=300/1000", "end=failed: the model gave up"} {
			assert.Contains(t, line, want)
		}
		for _, x := range sprint.RouteStats(routes, h.snap().Fleet) {
			if x.Route.Name == first {
				assert.Equal(t, 1, x.Failed)
				assert.Equal(t, 0, x.Provider, "the card's own failure is not the provider's")
			}
		}
		notes, _, err := h.st.B.NotesSince(h.ctx, "", 1000)
		require.NoError(t, err)
		named := false
		for _, n := range notes {
			named = named || n.Type == sprint.NWorkFailed && strings.HasPrefix(n.What, "route="+first+" model=prov-"+first+"/model-"+first+": ")
		}
		assert.True(t, named, "the failed work's judgment names the route and the model")
	}
}

// The route index (internal/sprint/route.go; tla/RouteIndex.tla) on the mem twin:
// each tier's array taken at a uint64 counter modulo its length, the counter the
// fleet table's route_index_<tier>.

// routeIndexOf is the fleet table's route index of the tier, "" when it has none.
func (h *harness) routeIndexOf(tier string) string {
	v, _ := h.snap().Fleet.Prop(sprint.PropRouteIndex(tier))
	return v
}

// dealtRoutes is the route of each card's first work card, in card order s1-1, s1-2, ...
func (h *harness) dealtRoutes(stream string, n int) []string {
	cards := h.workCards()
	var out []string
	for i := 1; i <= n; i++ {
		if wc := cards[fmt.Sprintf("%s-%d.w1", stream, i)]; wc != nil {
			out = append(out, wc.F(sprint.FieldRoute))
		}
	}
	return out
}

// tierHarness is routeHarness with flash routes a, b and c and the tiers' arrays.
func tierHarness(t *testing.T, tiers map[string][]string) *harness {
	h := routeHarness(t, route("a", "flash"), route("b", "flash"), route("c", "flash"), route("p", "pro"))
	h.m.SetTiers(tiers)
	return h
}

// Eight flash cards dealt in one tick over the array a,b,c,c take it in order twice,
// and the index is the number of cards dealt (RouteIndexAdvancesOncePerCard).
func TestTheTickDealsTheTiersArrayInOrder(t *testing.T) {
	t.Parallel()
	h := tierHarness(t, map[string][]string{"flash": {"a", "b", "c", "c"}})
	h.addReady("s1", 8, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	assert.Equal(t, []string{"a", "b", "c", "c", "a", "b", "c", "c"}, h.dealtRoutes("s1", 8))
	assert.Equal(t, "8", h.routeIndexOf("flash"))
	assert.Equal(t, "", h.routeIndexOf("pro"), "no pro card was dealt")
}

// A redeal leaves out the route the card was dealt on: from index 1 over a,b,c with b
// left out it takes c, and the index moves past both, to 3.
func TestARedealSkipsTheExcludedEntryAndMovesPastIt(t *testing.T) {
	t.Parallel()
	h := tierHarness(t, map[string][]string{"flash": {"b"}})
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	w1 := h.workCards()["s1-1.w1"]
	require.Equal(t, "b", w1.F(sprint.FieldRoute))
	require.Equal(t, "1", h.routeIndexOf("flash"))
	h.m.SetTiers(map[string][]string{"flash": {"a", "b", "c"}})
	gens := map[string]int{w1.ID: w1.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: gens, Who: "m1"}))
	h.run(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.run(DealStep(sprint.DealReq{}))
	wc := h.workCards()["s1-1.w1"]
	require.Equal(t, "m2", wc.Row, "dealt again")
	assert.Equal(t, "c", wc.F(sprint.FieldRoute), "index 1 is b, left out: the next entry")
	assert.Equal(t, "3", h.routeIndexOf("flash"), "moved past the entry skipped and the one taken")
}

// A pinned card bypasses the array and leaves the index where it is.
func TestAPinnedCardLeavesTheRouteIndex(t *testing.T) {
	t.Parallel()
	h := tierHarness(t, map[string][]string{"flash": {"a", "b"}})
	h.addReady("s1", 1, briefOf("flash", ""))
	h.addReady("s2", 2, briefOf("flash", "model: x/y\ntokens: 100\ndeadline: 60"))
	h.must(DealStep(sprint.DealReq{}))
	assert.Equal(t, "1", h.routeIndexOf("flash"), "the two pinned cards do not move it")
	assert.Equal(t, []string{"a"}, h.dealtRoutes("s1", 1))
	assert.Equal(t, []string{sprint.RoutePin, sprint.RoutePin}, h.dealtRoutes("s2", 2))
	h.addReady("s3", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	assert.Equal(t, []string{"b"}, h.dealtRoutes("s3", 1))
	assert.Equal(t, "2", h.routeIndexOf("flash"))
}

// A tier of one route always takes it, and the index still moves by one a card.
func TestATierOfOneRouteTakesItAndTheIndexMoves(t *testing.T) {
	t.Parallel()
	h := tierHarness(t, map[string][]string{"flash": {"a"}, "pro": {"p"}})
	h.addReady("s1", 3, briefOf("pro", ""))
	h.must(DealStep(sprint.DealReq{}))
	assert.Equal(t, []string{"p", "p", "p"}, h.dealtRoutes("s1", 3))
	assert.Equal(t, "3", h.routeIndexOf("pro"))
}

// An entry that names no enabled route of the tier (disabled since the array was
// set, or removed) is skipped, and the index moves past it.
func TestAnEntryNamingNoEnabledRouteIsSkipped(t *testing.T) {
	t.Parallel()
	h := tierHarness(t, map[string][]string{"flash": {"a", "b", "gone"}})
	off := route("b", "flash")
	off.Enabled = false
	h.m.SetRoutes([]sprint.Route{route("a", "flash"), off})
	h.addReady("s1", 2, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	assert.Equal(t, []string{"a", "a"}, h.dealtRoutes("s1", 2))
	assert.Equal(t, "4", h.routeIndexOf("flash"), "1 for the first card, then past b and gone to a: 3 more")
}

// A tick of 1000 cards over the arrays makes the round trips a tick of 1000 cards
// with no route makes, part by part: the index rides in the deal's batch
// (TestATicksPartsTripsArePinned pins the parts of a busy tick).
func TestATickOf1000CardsKeepsTheTripPin(t *testing.T) {
	t.Parallel()
	trips := func(routed bool) (map[string]int64, *harness) {
		h := newHarness(t)
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 500}))
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: 500}))
		if routed {
			h.m.SetRoutes([]sprint.Route{route("a", "flash"), route("b", "flash"), route("c", "flash")})
			h.m.SetTiers(map[string][]string{"flash": {"a", "b", "c", "c"}})
		}
		h.addReady("s1", 1000, briefOf("flash", ""))
		h.st.CheckTwin = nil
		h.startMachine()
		res := h.machine()
		got := map[string]int64{}
		for _, p := range res.Times {
			got[p.Table+"/"+p.Name] += p.Trips
		}
		return got, h
	}
	plain, _ := trips(false)
	routed, h := trips(true)
	assert.Equal(t, plain, routed, "the route index adds no round trip to any part")
	assert.Equal(t, "1000", h.routeIndexOf("flash"))
	n := map[string]int{}
	for _, r := range h.dealtRoutes("s1", 1000) {
		n[r]++
	}
	assert.Equal(t, map[string]int{"a": 250, "b": 250, "c": 500}, n, "exactly the array's share, every route")
}

// A read runs on its card's tier (the owner, 2026-10-01: "i think readers being
// conservatively the same tier as the work being done seems fine?"; route.go,
// readRouteOf; tla/RouteIndex.tla, THE READS): in one sprint a flash card's one
// read carries a flash route and a pro card's two reads pro routes (the owner,
// 2026-10-02, cost rule 4: "one cold read per flash card on a flash route; two per
// pro card"; ReadsNeeded), each drawn at its tier's rolling index, which the deal
// and the reads share; the route, model, budget and deadline are on the read card
// and in its packet, so a reader loop needs no --model.
func TestAReadRunsOnItsCardsTierAtThatTiersIndex(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"), route("flash-a", "flash"), route("flash-b", "flash"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.addReady("s2", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	// each deal took its tier's entry at index 0
	require.Equal(t, "pro-a", h.workCards()["s1-1.w1"].F(sprint.FieldRoute))
	require.Equal(t, "flash-a", h.workCards()["s2-1.w1"].F(sprint.FieldRoute))
	h.work("m1")
	h.work("m2")
	h.machine()
	s := h.snap()
	for id, want := range map[string]struct {
		tier   string
		routes []string
		index  string
	}{
		"s1-1": {"pro", []string{"pro-b", "pro-a"}, "3"}, // the deal took pro-a: the reads take entries 1 and 2
		"s2-1": {"flash", []string{"flash-b"}, "2"},      // the deal took flash-a: the one read takes entry 1
	} {
		tier := want.tier
		reads := s.Readers.Of(id)
		require.Len(t, reads, len(want.routes), "%s, a %s card, asked of %d readers", id, tier, len(want.routes))
		var got []string
		for _, rc := range reads {
			name := rc.F(sprint.FieldRoute)
			got = append(got, name)
			assert.Equal(t, "prov-"+name+"/model-"+name, rc.F(sprint.FieldModel), rc.ID)
			assert.Equal(t, "1000", rc.F(sprint.FieldTokens), rc.ID)
			assert.Equal(t, fmt.Sprint(routeSeconds), rc.F(sprint.FieldDeadline), rc.ID)
			p := sprint.PacketOf("", 0, rc, s.Work.Card(id), nil, nil)
			assert.Equal(t, "prov-"+name+"/model-"+name, p.Model, "the read's packet carries its model")
			assert.Equal(t, routeSeconds, p.Deadline)
		}
		assert.ElementsMatch(t, want.routes, got, "%s, a %s card, is read on %s routes", id, tier, tier)
		for _, rc := range reads {
			assert.Equal(t, tier, rc.F(sprint.FieldTier), "%s records the tier of its route", rc.ID)
		}
		v, _ := s.Fleet.Prop(sprint.PropRouteIndex(tier))
		assert.Equal(t, want.index, v, "the %s index moves once a card, work or read", tier)
	}
	h.clean("reads drawn")
}

// A primary in review waiting for reads while no enabled route serves its tier
// is held by the tier's "no route serves the tier" judgment at once, the deal's
// own (route.go, readRouteMissing; TickDeal), not at the unreported deadline; a
// route of the tier closes it. The card pins its model, so its work is dealt on
// the pin while its reads, on its tier, pro, have no route.
func TestReadsTheirCardsTierCannotServeAreJudgedAtOnce(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", "model: x/y\ntokens: 100\ndeadline: 60"))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	open := h.a2Open(sprint.NNoRoute)
	require.Len(t, open, 1, "the card's tier, pro, has no route")
	assert.Equal(t, sprint.StreamSubject(sprint.TierSubject("pro")), open[0].Subject())
	assert.Contains(t, open[0].Note.What, "the tier of the work its reads read")
	assert.Contains(t, open[0].Note.Primaries, "s1-1")
	h.machine()
	assert.Len(t, h.a2Open(sprint.NNoRoute), 1, "written once, not every tick")
	assert.Equal(t, 1, h.notesOf(sprint.NNoRoute))
	h.m.SetRoutes([]sprint.Route{route("flash-a", "flash"), route("pro-a", "pro")})
	h.machine()
	assert.Empty(t, h.a2Open(sprint.NNoRoute), "a route of the card's tier closes it")
}

// One path asks (commit 255180e2; fleet pass 7, 2026-10-01): the finish of reworked work
// asks no reader; the machine's ask, in the tick the finish wakes, asks two different
// readers round the readers (a pro card's two reads), each read with the route it draws.
// A read the finish asked itself carried no route, and no reader could start it.
func TestAReadOfReworkedWorkCarriesARoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"), route("flash-a", "flash"), route("flash-b", "flash"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	h.finishAttempt("s1-1", false, pushedA)
	h.machine()
	first := h.snap().Readers.Of("s1-1")
	require.Len(t, first, 2, "attempt 1 asked of two readers")
	h.must(ReadStep(sprint.ReadReq{As: first[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{first[0].ID}}}))
	h.must(ReadStep(sprint.ReadReq{As: first[1].Row, Verdict: "broken", Finding: "f:1", Sel: sprint.Sel{IDs: []string{first[1].ID}}}))
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	h.finishAttempt("s1-1", false, pushedB)
	pr := h.snap().Work.Card("s1-1")
	require.Equal(t, 2, pr.Int("attempt"))
	assert.Empty(t, readsAt(h.snap(), pr), "the finish asks no reader")
	h.machine()
	reads := readsAt(h.snap(), h.snap().Work.Card("s1-1"))
	require.Len(t, reads, 2, "attempt 2 asked of two readers by the machine's ask")
	var who []string
	for _, rc := range reads {
		who = append(who, rc.F("reader"))
		assert.NotEmpty(t, rc.F(sprint.FieldRoute), "%s has a route", rc.ID)
		assert.NotEmpty(t, rc.F(sprint.FieldModel), "%s has a model", rc.ID)
		assert.NotEmpty(t, rc.F(sprint.FieldDeadline), "%s has a deadline", rc.ID)
	}
	assert.NotEqual(t, who[0], who[1], "asked twice of one reader")
	assert.ElementsMatch(t, sprint.Split(h.snap().Work.Card("s1-1").F("asked")), who, "the primary's asked field names the readers of attempt 2")
	h.clean("reworked work asked with routes")
}
