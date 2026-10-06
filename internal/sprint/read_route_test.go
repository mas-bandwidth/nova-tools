package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A heavy read needs a reader, not a route (docs/SPEC-SPRINT.md section 6; the card
// pr-wire-server-restart, 2026-10-05, stalled in review under "no enabled route serves
// tier heavy" while the fleet's table held only flash and pro routes and the friends
// who read heavy were up). A friend's or a bud's reader (reader-<name> for a friend
// the snapshot seats) brings its own model: it is asked the read with no route, its verdict's
// usage line names the model and harness the read card records, and the tier's no-route
// judgment is not raised. A fleet reader still draws a route: with none of the tier it
// is asked nothing, and with no other reader up the judgment is raised as before.
func TestAHeavyCardIsReadByAFriendReaderWithNoHeavyRoute(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	setup := func(up ...string) *world {
		w := newWorld(t, "reader-m1", "reader-amy", "reader-bob")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
		// amy and bob are friends: the tick hands the snapshot their seats
		w.s.Friends = []FriendSeat{{Name: "amy", Width: 1, Status: Up}, {Name: "bob", Width: 1, Status: Up}}
		w.s.Routes = []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
			{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "q", Enabled: true},
		}
		putReview(w, "s1-1", "s1-1: heavy work tier: heavy\n\nThe task.", 1, 1, head)
		w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RouteHeavy
		w.s.ReaderStates = map[string]string{"reader-m1": ReaderUp, "reader-amy": ReaderDown, "reader-bob": ReaderDown}
		for _, rd := range up {
			w.s.ReaderStates[rd] = ReaderUp
		}
		require.Equal(t, cardhdr.RouteHeavy, w.s.readTierOf(w.s.Work.Card("s1-1")))
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

	w := setup("reader-amy", "reader-bob")
	tick(w)
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 1, "the first read is asked, one at a time")
	first := reads[0]
	assert.Contains(t, []string{"reader-amy", "reader-bob"}, first.F("reader"), "a friend reader, never the fleet reader with no heavy route")
	assert.Equal(t, "", first.F(FieldRoute), "a friend brings her model: no route is drawn")
	assert.Equal(t, cardhdr.RouteHeavy, first.F(FieldTier))
	assert.False(t, held(w), "a reader up serves heavy: no no-route judgment")

	w.must(Read(w.s, ReadReq{As: first.Row, Verdict: "ok", Sel: Sel{IDs: []string{first.ID}},
		Usage: "model=anthropic/claude-opus-5-5 wall=300s harness=claude account=amy"}))
	rc := w.s.Readers.Card(first.ID)
	assert.Equal(t, "anthropic/claude-opus-5-5", rc.F(FieldModel), "the read records the reader's model from its usage line")
	assert.Equal(t, "claude", rc.F(FieldHarness), "and its harness")

	tick(w)
	reads = readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2, "both reads are asked")
	assert.ElementsMatch(t, []string{"reader-amy", "reader-bob"}, readerNames(reads))
	assert.False(t, held(w), "still no no-route judgment")
	assert.Empty(t, w.notesOf(NCannotAsk))

	// the control: no reader that brings its own model is up, and the fleet reader has
	// no route of heavy to draw: the judgment is raised as before, and nothing is asked
	c := setup()
	tick(c)
	assert.Empty(t, readsAt(c.s, c.s.Work.Card("s1-1"), 1), "the fleet reader is not asked a heavy read with no heavy route")
	assert.True(t, held(c), "no reader and no route serve heavy: the tier's judgment")
}
