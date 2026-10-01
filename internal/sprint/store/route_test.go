package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The deal's route (internal/sprint/route.go; tla/DirtyTick.tla, Deal's route
// guard) on the mem twin: the card's tier resolved through the store's routes at
// deal time, written on the work card and handed in its packet.

// routeSeconds is a route's deadline: seconds, as nova-config's route row holds it.
var routeSeconds = 600

func route(name, tier string, weight int) sprint.Route {
	return sprint.Route{Name: name, Tier: tier, Provider: "prov-" + name, Model: "model-" + name, Tokens: 1000, Deadline: routeSeconds, Weight: weight, Enabled: true}
}

// briefOf is a brief whose line 1 names the tier, and whose header carries extra.
func briefOf(tier, extra string) string {
	return "c: the work (s1) tier: " + tier + "\n" + extra + "\nThe task.\n"
}

// routeHarness is the harness with its two members up and the store's routes.
func routeHarness(t *testing.T, routes ...sprint.Route) *harness {
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
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
	h := routeHarness(t, route("pro-a", "pro", 1), route("pro-b", "pro", 1), route("flash-a", "flash", 1))
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

func TestAPinnedCardRunsOnItsPinAndBypassesTheDraw(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro", 1))
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
	h := routeHarness(t, route("flash-a", "flash", 1), func() sprint.Route { r := route("pro-off", "pro", 1); r.Enabled = false; return r }())
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

	h.m.SetRoutes([]sprint.Route{route("flash-a", "flash", 1), route("pro-a", "pro", 1)})
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

// The draw over many deals on the twin: weights 1:1:2 within four standard
// deviations of their binomial means over 400 deals; the same clock and the same
// cards draw the same routes, and another clock (another tick) draws others.
func TestTheDrawFollowsTheRoutesWeights(t *testing.T) {
	t.Parallel()
	rs := []sprint.Route{route("pro-a", "pro", 1), route("pro-b", "pro", 1), route("pro-c", "pro", 2)}
	draws := func(later time.Duration) map[string]string {
		h := routeHarness(t, rs...)
		h.tick(later)
		h.addReady("s1", 400, briefOf("pro", ""))
		h.must(DealStep(sprint.DealReq{}))
		out := map[string]string{}
		for id, wc := range h.workCards() {
			out[id] = wc.F(sprint.FieldRoute)
		}
		return out
	}
	a := draws(0)
	got := map[string]int{}
	for _, r := range a {
		got[r]++
	}
	require.Equal(t, 400, got["pro-a"]+got["pro-b"]+got["pro-c"], "every card drew a pro route: %v", got)
	assert.InDelta(t, 100, got["pro-a"], 35, "weight 1 of 4 over 400: %v", got)
	assert.InDelta(t, 100, got["pro-b"], 35, "weight 1 of 4 over 400: %v", got)
	assert.InDelta(t, 200, got["pro-c"], 40, "weight 2 of 4 over 400: %v", got)
	assert.Equal(t, a, draws(0), "the same clock and cards draw the same routes")
	diff := 0
	for id, r := range draws(time.Second) {
		if a[id] != r {
			diff++
		}
	}
	assert.Greater(t, diff, 100, "another tick's clock draws other routes for many cards")
}

// A work card withdrawn by its member going down is dealt again leaving out the
// route it was dealt on while another remains; with one route, the same one.
func TestARedealLeavesOutTheRouteItWasDealtOn(t *testing.T) {
	t.Parallel()
	for _, two := range []bool{true, false} {
		rs := []sprint.Route{route("pro-a", "pro", 1)}
		if two {
			rs = append(rs, route("pro-b", "pro", 1))
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

// Three routes, three attempts each failed by the provider: every attempt leaves out
// every route drawn before, so the three attempts run on the three routes.
func TestTheExclusionCoversEveryAttempt(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro", 1), route("pro-b", "pro", 1), route("pro-c", "pro", 1))
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
			Report: cardhdr.EndProvider + ": 529", Who: wc.Row}))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Who: "tester"}))
	}
	assert.ElementsMatch(t, []string{"pro-a", "pro-b", "pro-c"}, seen)
}

// A card admitted before the lint whose model lines the deal cannot read (a pin
// with no budget, an unknown tier) is not dealt, and is judged once under the tier
// its line 1 names: never skipped in silence.
func TestACardWhoseModelLinesCannotBeReadIsJudgedUnderItsTier(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro", 1))
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
	h := routeHarness(t, route("pro-a", "pro", 1), route("pro-b", "pro", 1))
	h.startMachine()
	off := route("pro-a", "pro", 1)
	off.Enabled = false
	h.m.SetRoutes([]sprint.Route{off, route("pro-b", "pro", 1)})
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
	h := routeHarness(t, route("pro-a", "pro", 1))
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
		{route("pro-a", "pro", 1), route("pro-b", "pro", 1)},
		{route("pro-a", "pro", 1)},
	} {
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.must(DealStep(sprint.DealReq{}))
		w1 := h.workCards()["s1-1.w1"]
		first := w1.F(sprint.FieldRoute)
		gens := map[string]int{w1.ID: w1.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: w1.Row, Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: gens, Who: w1.Row}))
		h.must(FinishStep(sprint.FinishReq{As: w1.Row, Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: gens, Failed: true,
			Report: cardhdr.EndProvider + ": 529 overloaded", Usage: "wall=12.00s budget=300/1000", Who: w1.Row}))
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
			"usage=wall=12.00s budget=300/1000", "end=failed: " + cardhdr.EndProvider} {
			assert.Contains(t, line, want)
		}
		for _, x := range sprint.RouteStats(routes, h.snap().Fleet) {
			if x.Route.Name == first {
				assert.Equal(t, 1, x.Failed)
				assert.Equal(t, 1, x.Provider, "the provider's failure counts apart")
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
