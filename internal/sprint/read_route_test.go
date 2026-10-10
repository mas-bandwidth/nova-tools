package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
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
// is asked nothing, and with no other reader up the judgment is raised as before. Under the
// interim rule (the owner, 2026-10-06 7:41 PM ET: "let pro do it") a heavy read collapses to
// pro: here no pro route is enabled either, so the same holds of the tier pro.
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
			{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "q", Enabled: false},
		}
		putReview(w, "s1-1", "s1-1: heavy work tier: heavy\n\nThe task.", 1, 1, head)
		w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RouteHeavy
		w.s.ReaderStates = map[string]string{"reader-m1": ReaderUp, "reader-amy": ReaderDown, "reader-bob": ReaderDown}
		for _, rd := range up {
			w.s.ReaderStates[rd] = ReaderUp
		}
		require.Equal(t, cardhdr.RoutePro, w.s.readTierOf(w.s.Work.Card("s1-1")), "a heavy read collapses to pro (the interim rule)")
		return w
	}
	held := func(w *world) bool {
		for _, n := range w.notesOf(NNoRoute) {
			if n.Stream == TierSubject(cardhdr.RoutePro) && contains(n.Primaries, "s1-1") {
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
	require.Len(t, reads, 2, "both reads are asked together")
	assert.ElementsMatch(t, []string{"reader-amy", "reader-bob"}, readerNames(reads), "the friend readers, never the fleet reader with no heavy route")
	for _, rc := range reads {
		assert.Equal(t, "", rc.F(FieldRoute), "a friend brings her model: no route is drawn")
		assert.Equal(t, cardhdr.RoutePro, rc.F(FieldTier), "read on pro (the interim rule)")
	}
	first := reads[0]
	assert.False(t, held(w), "a reader up serves heavy: no no-route judgment")

	w.must(Read(w.s, ReadReq{As: first.Row, Verdict: "ok", Sel: Sel{IDs: []string{first.ID}},
		Usage: "model=anthropic/claude-opus-5-5 wall=300s harness=claude account=amy"}))
	rc := w.s.Readers.Card(first.ID)
	assert.Equal(t, "anthropic/claude-opus-5-5", rc.F(FieldModel), "the read records the reader's model from its usage line")
	assert.Equal(t, "claude", rc.F(FieldHarness), "and its harness")

	tick(w)
	reads = readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2, "no third read is asked")
	assert.False(t, held(w), "still no no-route judgment")
	assert.Empty(t, w.notesOf(NCannotAsk))

	// the control: no reader that brings its own model is up, and the fleet reader has
	// no route of heavy to draw: the judgment is raised as before, and nothing is asked
	c := setup()
	tick(c)
	assert.Empty(t, readsAt(c.s, c.s.Work.Card("s1-1"), 1), "the fleet reader is not asked a heavy read with no heavy route")
	assert.True(t, held(c), "no reader and no route serve heavy: the tier's judgment")
}

// A read its reader refused to launch for want of a route is not a read (docs/SPEC-SPRINT.md
// section 6, a read refused is not a read; 2026-10-06: every fleet reader was asked heavy
// reads with no route on the card, each refusal "launch refused: card X has no route (model
// "" tokens "" deadline 0s)" was counted toward the re-ask bound as a read, and the readers
// were spent at the attempt). Each refusal is retired off its reader, counted toward no
// bound, and asked again (its reader once more under .g1, the interim rule); once too few
// readers are free for it, the tier's one judgment holds it in review, no cannot-ask
// judgment is raised, and the readers stay up.
func TestAReaderRefusedForNoRouteIsNotSweptAway(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	w := newWorld(t, "reader-m1", "reader-m2")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.s.ReaderStates = map[string]string{"reader-m1": ReaderUp, "reader-m2": ReaderUp}
	// the store holds no route, so the sprint believes every reader runs its own: the
	// read cards carry none, and the fleet readers' members have no override
	putReview(w, "s1-1", "s1-1: heavy work tier: heavy\n\nThe task.", 1, 1, head)
	w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RouteHeavy
	tick := func() {
		w.t.Helper()
		p, _ := TickAsk(w.s, TickReq{})
		w.must(p)
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
	}

	refused := 0
	for range 10 {
		tick()
		reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
		if len(reads) == 0 {
			break
		}
		for _, rc := range reads {
			why := `no verdict (ran=false verdict=""): launch refused: card ` + rc.ID + ` has no route (model "" tokens "" deadline 0s) and this member no override for what is missing`
			w.must(Read(w.s, ReadReq{As: rc.Row, Return: true, Reason: why, Sel: Sel{IDs: []string{rc.ID}}}))
			c := w.s.Readers.Card(rc.ID)
			require.NotNil(t, c)
			assert.Equal(t, RetiredByRefused, c.F("retired_by"), "a refused read is retired off its reader")
			assert.Empty(t, c.F(FieldReasked), "a refusal counts toward no re-ask bound")
			refused++
		}
	}
	assert.Equal(t, 4, refused, "both readers asked together, each once more under .g1 after its refusal, then no more: none is free for it")

	tick()
	tick()
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, Review, pr.Col, "the card stays in review")
	assert.Empty(t, readsAt(w.s, pr, 1), "nothing is asked again at the attempt")
	assert.True(t, w.s.refusedNoRoute(pr))
	var held []Note
	for _, n := range w.notesOf(NNoRoute) {
		if n.Stream == TierSubject(cardhdr.RoutePro) && contains(n.Primaries, "s1-1") { // heavy is read on pro (the interim rule)
			held = append(held, n)
		}
	}
	require.Len(t, held, 1, "one judgment for the tier, written once")
	assert.Contains(t, held[0].What, "refused its read for want of a route")
	assert.Empty(t, w.notesOf(NCannotAsk), "a refusal is no read: no cannot-ask judgment")
	assert.Equal(t, map[string]string{"reader-m1": ReaderUp, "reader-m2": ReaderUp}, w.s.ReaderStates, "the readers stay up")
	assert.ElementsMatch(t, []string{"reader-m1", "reader-m2"}, w.s.UpReaders())
}
