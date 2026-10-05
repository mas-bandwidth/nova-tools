package sprint

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A heavy card is read by friends who bring their own models. The fleet's
// route table holds flash and pro only. Two friend readers up: both reads are
// asked, each records that reader's model and harness, and no no-route
// judgment is raised. With no reader up, the judgment is raised as today.
func TestAHeavyCardIsReadByAFriendReaderWithNoHeavyRoute(t *testing.T) {
	t.Parallel()
	t.Run("two friends up", func(t *testing.T) {
		t.Parallel()
		w := heavyFriendWorld(t, true)
		w.part(TickDeal, TickReq{})
		require.Empty(t, routeJudgments(w), "a friend reader up serves heavy with no heavy route")
		w.part(TickAsk, TickReq{})
		pr := w.s.Work.Card("s1-1")
		first := liveReadsAt(w.s, pr, 1)
		require.Len(t, first, 1, "the first read is asked alone")
		assertBrought(t, w, first[0])
		w.must(Read(w.s, ReadReq{As: first[0].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{first[0].ID}}}))
		w.part(TickAsk, TickReq{})
		both := liveReadsAt(w.s, pr, 1)
		require.Len(t, both, 2, "the second read is asked once the first is ok")
		seen := map[string]bool{}
		for _, rc := range both {
			assertBrought(t, w, rc)
			seen[rc.F("reader")] = true
		}
		assert.Equal(t, map[string]bool{"reader-rowan-mas": true, "reader-johnny": true}, seen)
		w.part(TickDeal, TickReq{})
		require.Empty(t, routeJudgments(w))
		_, heavyMoved := w.s.Fleet.Prop(PropRouteIndex(cardhdr.RouteHeavy))
		assert.False(t, heavyMoved, "a friend read draws no heavy route and moves no index")
	})
	t.Run("no reader up", func(t *testing.T) {
		t.Parallel()
		w := heavyFriendWorld(t, false)
		w.part(TickDeal, TickReq{})
		ns := routeJudgments(w)
		require.Len(t, ns, 1)
		assert.Contains(t, ns[0].What, "no enabled route serves tier heavy")
		assert.Contains(t, ns[0].What, "the tier of the work its reads read")
		assert.Empty(t, liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1))
	})
}

// heavyFriendWorld is the in-memory twin: flash and pro routes only, a heavy
// primary in review, and two friend readers whose usage lines name a model
// and a harness. up says both are up; otherwise neither is.
func heavyFriendWorld(t *testing.T, up bool) *world {
	t.Helper()
	const (
		rowan  = "reader-rowan-mas"
		johnny = "reader-johnny"
	)
	w := newWorld(t, rowan, johnny)
	flash := Route{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "flash", Enabled: true, Tokens: 1000}
	pro := Route{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "pro", Enabled: true, Tokens: 1000}
	flash.Deadline, pro.Deadline = 600, 600 // seconds, as the route row holds it
	w.s.Routes = []Route{flash, pro}
	putReview(w, "s1-1", "s1-1: the card (s1) tier: heavy\n", 1, 1, "heavy-head")
	w.s.Readers.SetProp(PropReaderUsage, strings.Join([]string{
		johnny + "\tmodel=xai/grok-4 harness=grok",
		rowan + "\tmodel=anthropic/claude-opus-5-5 harness=claude",
	}, "\n"))
	state := ReaderDown
	if up {
		state = ReaderUp
	}
	w.s.ReaderStates = map[string]string{rowan: state, johnny: state}
	return w
}

func routeJudgments(w *world) []Note { return w.notesOf(NNoRoute) }

func assertBrought(t *testing.T, w *world, rc *Card) {
	t.Helper()
	want := map[string][2]string{
		"reader-rowan-mas": {"anthropic/claude-opus-5-5", "claude"},
		"reader-johnny":    {"xai/grok-4", "grok"},
	}
	model := want[rc.F("reader")]
	require.NotEmpty(t, model[0], rc.F("reader"))
	assert.Empty(t, rc.F(FieldRoute), rc.ID)
	assert.Equal(t, model[0], rc.F(FieldModel), rc.ID)
	assert.Equal(t, model[1], rc.F(FieldHarness), rc.ID)
	assert.Equal(t, cardhdr.RouteHeavy, rc.F(FieldTier), rc.ID)
	assert.Equal(t, cardhdr.RouteHeavy, w.s.readTierOf(w.s.Work.Card("s1-1")))
}
