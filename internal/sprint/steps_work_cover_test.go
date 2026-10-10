package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AddEach is one add of several streams (steps_work.go): each stream's add on the same
// pre-state, their plans as one. The sprint-done judgment every add of it answers is kept
// once, on the first unit that names it; an add whose stream is refused contributes its
// refusal and moves nothing, and its siblings are admitted as their own add would.
func TestStepsWorkCoverAddEach(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Open = []Open{{Key: OpenKey("n1", SprintSubject), Note: Note{ID: "n1", Kind: Judgment, Type: NSprintDone}}}
	p := AddEach(w.s, []AddReq{
		{Stream: "s1", IDs: []string{"s1-1"}},
		{Stream: "s2", IDs: []string{"s2-1"}},
	})
	require.Empty(t, p.Refused, "the add of two streams: %v", p.Refused)
	require.Len(t, p.Units, 2, "one unit per admitted card, in the adds' order")
	assert.Equal(t, "s1-1", p.Units[0].Key)
	assert.Equal(t, "s2-1", p.Units[1].Key)
	assert.Len(t, p.Units[0].Closes, 1, "the first unit answers the sprint-done judgment")
	assert.Empty(t, p.Units[1].Closes, "one open judgment is never closed twice")
	w.must(p)
	assert.Equal(t, Ready, w.state("s1-1"))
	assert.Equal(t, Ready, w.state("s2-1"))

	q := AddEach(w.s, []AddReq{
		{Stream: "s3", IDs: []string{"s3-1"}},
		{Stream: "bad stream", IDs: []string{"x-1"}},
	})
	require.Len(t, q.Refused, 1, "%v", q.Refused)
	assert.Equal(t, "x-1", q.Refused[0].Key)
	assert.Contains(t, q.Refused[0].Why, `stream "bad stream" wants letters, digits, _ and -`)
	require.Len(t, q.Units, 1, "the well-named stream is admitted all the same")
	assert.Equal(t, "s3-1", q.Units[0].Key)
}

// missingNeeds names the dependencies with no record at all (steps_work.go): a card on the
// table is none, and a kept dropped record is none either, for Resolve reads the unplaced
// dependencies too (its distinct case is ResolveExtras).
func TestStepsWorkCoverMissingNeeds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	w.s.Work.Put(&Card{ID: "old-1", Fields: map[string]string{"outcome": "dropped"}})
	for _, tc := range []struct {
		name  string
		needs []string
		want  []string
	}{
		{"a name with no record is missing", []string{"s1-1", "old-1", "ghost"}, []string{"ghost"}},
		{"a card on the table and a kept record are none missing", []string{"s1-1", "old-1"}, nil},
		{"no needs miss nothing", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, missingNeeds(w.s, tc.needs))
		})
	}
}

// ResolveExtras is the needs a resolve must read as records (steps_work.go): the not-placed
// ones of every waiting primary, a kept dropped record and an unrecorded name alike, each
// listed once however many waiting cards need it; a need placed on the table is none of
// its, and a world with nothing waiting has nothing.
func TestStepsWorkCoverResolveExtras(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	w.s.Work.Put(&Card{ID: "old-1", Fields: map[string]string{"outcome": "dropped"}})
	w.s.Work.Put(&Card{ID: "p1", Row: "s1", Col: Waiting, Fields: map[string]string{"kind": "primary", "needs": "s1-1,old-1,ghost"}})
	w.s.Work.Put(&Card{ID: "p2", Row: "s1", Col: Waiting, Fields: map[string]string{"kind": "primary", "needs": "ghost,old-1"}})
	assert.Equal(t, []string{"old-1", "ghost"}, ResolveExtras(w.s), "each extra once, in the first waiting card's order")

	t.Run("nothing waits, nothing is extra", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ResolveExtras(newWorld(t).s))
	})
}

// escalate deals the primary a new attempt on the next tier (steps_work.go), the machine's
// step at the redeal bound below the ceiling: the bound attempt's card retires as escalated,
// the new one enters the member's ready queue told why, the primary goes working on it and
// records the tier every later deal draws from. It refuses when it cannot cut the new
// attempt's card, and then loads no member.
func TestStepsWorkCoverEscalate(t *testing.T) {
	t.Parallel()
	bound := func(t *testing.T) (*world, *Card, *Card, map[string]int) {
		t.Helper()
		w := newWorld(t)
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
		c := w.s.Work.Card("s1-1")
		c.Fields["attempt"] = "3"
		prev := &Card{ID: "s1-1.w3", Row: "m1", Col: Withdrawn, Fields: map[string]string{
			"kind": "work", "primary": "s1-1", "stream": "s1", "attempt": "3", "gen": "1"}}
		w.s.Fleet.Put(prev)
		return w, c, prev, map[string]int{"m1": 0}
	}
	why := "attempt 3 reached its bound on flash"
	t.Run("the next tier is dealt and the bound attempt retires", func(t *testing.T) {
		t.Parallel()
		w, c, prev, q := bound(t)
		u, refused := escalate(w.s, c, prev, cardhdr.RoutePro, why, "m1", q, routeIndexesOf(w.s))
		require.Equal(t, "", refused, "%+v", u)
		assert.Equal(t, 1, q["m1"], "the member dealt to counts the new card")
		assert.Contains(t, u.Moved, "card=s1-1.w4 member=m1")
		assert.Contains(t, u.Moved, "; escalated flash -> pro: "+why)
		w.do(Plan{Units: []Unit{u}})
		wc := w.s.Fleet.Card("s1-1.w4")
		require.NotNil(t, wc, "the new attempt's card")
		assert.Equal(t, Ready, wc.Col)
		assert.Equal(t, "escalated from flash to pro: "+why, wc.F("why"), "the new attempt's card is told why")
		assert.Equal(t, "escalation", w.s.Fleet.Card("s1-1.w3").F("retired_by"), "the bound attempt retires")
		assert.Equal(t, "pro", w.s.Work.Card("s1-1").F(FieldTierNow), "later deals draw from the tier it rose to")
		assert.Equal(t, "4", w.s.Work.Card("s1-1").F("attempt"))
		assert.Equal(t, Working, w.state("s1-1"))
	})
	t.Run("a next-attempt card that exists already refuses the escalation", func(t *testing.T) {
		t.Parallel()
		w, c, prev, q := bound(t)
		w.s.Fleet.Put(&Card{ID: "s1-1.w4", Row: "m1", Col: Ready, Fields: map[string]string{"kind": "work", "primary": "s1-1"}})
		u, refused := escalate(w.s, c, prev, cardhdr.RoutePro, why, "m1", q, routeIndexesOf(w.s))
		assert.Equal(t, "work card s1-1.w4 exists already", refused)
		assert.Empty(t, u.Key, "the refusal writes nothing")
		assert.Equal(t, 0, q["m1"], "a refused deal loads no member")
	})
}

// takeEnded is the unit of a take that ended with no work to judge (steps_work.go): the
// provider failed it, or its child left no result. The work card withdraws with the ended
// take's stamps and record and its primary goes back to ready; the tick's deal places it
// again counting the take against the redeal bound. No failed-work judgment is written and
// the primary's failed count does not move: such a take is never the card's.
func TestStepsWorkCoverTakeEnded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, report, why, line string }{
		{"the provider failed the take", "provider failure: dial timeout", "the provider failed the take", "dial timeout"},
		{"the child left no result", "no result: the child wrote nothing", "the child left no result", "no result: the child wrote nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
			wc := w.s.Work.Card("s1-1").F("work")
			member := w.s.Fleet.Card(wc).Row
			w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{wc}}, Gens: w.gens(wc)}))
			p := Finish(w.s, FinishReq{As: member, Sel: Sel{IDs: []string{wc}}, Gens: w.gens(wc), Failed: true, Report: tc.report})
			require.Len(t, p.Units, 1, "%+v", p)
			assert.Contains(t, p.Units[0].Moved, tc.why+"; s1-1 working -> ready")
			w.must(p)
			c := w.s.Fleet.Card(wc)
			assert.Equal(t, Provider, c.Col, "the take's card withdraws into its own provider column for the next deal")
			assert.NotEqual(t, "", c.F(FieldTakeEnded), "the ended take counts against the redeal bound")
			assert.Equal(t, tc.line, c.F(FieldProviderError), "the card keeps the ended take's line")
			takes, numbers := ProviderTakes(c)
			require.Len(t, takes, 1, "the ended take's own record")
			assert.Equal(t, []int{1}, numbers, "its first take: none redealt yet")
			assert.Equal(t, member, takes[0].Member)
			assert.Equal(t, tc.line, takes[0].Error)
			assert.Equal(t, Ready, w.state("s1-1"), "the primary goes back to ready, never to review")
			assert.Equal(t, 0, w.s.Work.Card("s1-1").Int("failed"), "such a take is never the card's fault")
			assert.Empty(t, w.notesOf(NWorkFailed), "no failed-work judgment is written")
		})
	}
	t.Run("a decided no-result is nova-decide's class in its place", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		wc := w.s.Work.Card("s1-1").F("work")
		member := w.s.Fleet.Card(wc).Row
		w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{wc}}, Gens: w.gens(wc)}))
		u := takeEnded(w.s, w.s.Fleet.Card(wc), w.s.Work.Placed("s1-1"),
			FinishReq{Report: "no result: the child wrote nothing"}, cardhdr.EndNoResult, true)
		assert.Contains(t, u.Moved, "nova-decide classed the take "+decide.ClassNoResult)
	})
}
