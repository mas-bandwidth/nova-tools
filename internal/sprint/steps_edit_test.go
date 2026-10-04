package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The coordinator's edits of a STOPPED sprint's unstarted primaries (the
// owner, 2026-10-01: "What other things should you be able to do to mutate a
// stopped sprint" / "I don't want you manually hopping in and working around
// it and doing manual stuff."): refused on a RUNNING machine, and for a card
// that has started, one case per state.

// editWorld is stream a with a-1 ready and a-2 waiting on it.
func editWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-1"}, Brief: "old one", Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-2"}, Needs: []string{"a-1"}, Brief: "old two", Who: "coordinator"}))
	require.Equal(t, Ready, w.s.Work.Placed("a-1").Col)
	require.Equal(t, Waiting, w.s.Work.Placed("a-2").Col)
	return w
}

// startedCases is a started primary in each state an edit refuses: dealt once
// and back in ready, working, in review, merging and landed.
var startedCases = []struct {
	name, col string
	attempt   string
}{
	{"ready with an attempt dealt", Ready, "1"},
	{"working", Working, "1"},
	{"review", Review, "1"},
	{"merging", Merging, "1"},
	{"landed", Landed, "1"},
}

func TestBriefReplacesTheBriefOfAnUnstartedPrimary(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a-1", "a-2"} {
		w := editWorld(t)
		before := *w.s.Work.Placed(id)
		w.must(Brief(w.s, briefOne(id, "new brief")))
		c := w.s.Work.Placed(id)
		assert.Equal(t, "new brief", c.F("brief"), id)
		assert.Equal(t, before.Row, c.Row, id)
		assert.Equal(t, before.Col, c.Col, id)
		assert.Equal(t, before.Score, c.Score, id)
		assert.Equal(t, before.Fields["needs"], c.F("needs"), id)
	}
}

// briefOne is the request replacing one primary's brief.
func briefOne(id, brief string) BriefReq {
	return BriefReq{Cards: []BriefCard{{ID: id, Brief: brief}}, Who: "coordinator"}
}

// tierOne is the request re-tiering one primary.
func tierOne(id, tier string) BriefReq {
	return BriefReq{Cards: []BriefCard{{ID: id, Tier: tier}}, Who: "coordinator"}
}

// A waiting or ready primary with no work card dealt takes a new brief while
// the machine runs: only a card dealt or in flight keeps its brief, so the
// briefs of cards in line are replaced without a stop window
// (docs/SPEC-SPRINT.md, the brief verb). A dealt card is still refused.
func TestBriefReplacesAWaitingCardWhileRunning(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a-1", "a-2"} {
		w := editWorld(t)
		w.s.Running = true
		before := *w.s.Work.Placed(id)
		w.must(Brief(w.s, briefOne(id, "new brief")))
		c := w.s.Work.Placed(id)
		assert.Equal(t, "new brief", c.F("brief"), id)
		assert.Equal(t, before.Col, c.Col, id)
		assert.Equal(t, before.Score, c.Score, id)
		assert.Equal(t, before.Fields["needs"], c.F("needs"), id)
	}
	for _, tc := range startedCases {
		w := editWorld(t)
		w.s.Running = true
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		p := Brief(w.s, briefOne("a-1", "new"))
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, "keeps its brief", tc.name)
	}
}

// Several briefs in one request (brief --dir) are one unit each, all on one
// snapshot; a card refused is named, and a card named twice is refused.
func TestBriefReplacesSeveralBriefsInOneRequest(t *testing.T) {
	t.Parallel()
	w := editWorld(t)
	w.s.Running = true
	w.must(Brief(w.s, BriefReq{Cards: []BriefCard{{ID: "a-1", Brief: "one"}, {ID: "a-2", Brief: "two"}}, Who: "coordinator"}))
	assert.Equal(t, "one", w.s.Work.Placed("a-1").F("brief"))
	assert.Equal(t, "two", w.s.Work.Placed("a-2").F("brief"))
	p := Brief(w.s, BriefReq{Cards: []BriefCard{{ID: "a-1", Brief: "x"}, {ID: "zz", Brief: "y"}, {ID: "a-1", Brief: "z"}}, Who: "coordinator"})
	require.Len(t, p.Refused, 2)
	assert.Contains(t, p.Refused[0].Why, "no primary zz")
	assert.Equal(t, "a-1", p.Refused[1].Key)
	assert.Contains(t, p.Refused[1].Why, "named twice")
	w.clean("after the briefs")
}

func TestBriefIsRefusedForAStartedCard(t *testing.T) {
	t.Parallel()
	for _, tc := range startedCases {
		w := editWorld(t)
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		p := Brief(w.s, briefOne("a-1", "new"))
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, "a-1 is "+tc.col, tc.name)
		assert.Contains(t, p.Refused[0].Why, "keeps its brief", tc.name)
	}
}

// A brief refused for a card dealt names what changes it instead, by the
// lifecycle's moves, and does so on a RUNNING machine too, where stopping it
// would not help: rework --fix from review (after a return from merging), drop
// and a new card from any open state, a new card after landing.
func TestABriefRefusedForAStartedCardNamesItsRemedy(t *testing.T) {
	t.Parallel()
	drop := "nova-sprint drop a-1 --reason '<why>', then nova-sprint add --stream a <new id> --brief-file <path>"
	rework := "nova-sprint rework a-1 --fix '<what changes>'"
	want := map[string][]string{
		"ready with an attempt dealt": {"run: " + drop},
		"working":                     {"run: " + drop, "once it finishes (review), " + rework},
		"review":                      {"run: " + rework, drop},
		"merging":                     {"run: nova-sprint return a-1 --reason '<why>', then " + rework, drop},
		"landed":                      {"run: nova-sprint add --stream a <new id> --brief-file <path> for the change"},
	}
	for _, tc := range startedCases {
		require.NotEmpty(t, want[tc.name], tc.name)
		for _, running := range []bool{false, true} {
			w := editWorld(t)
			w.s.Running = running
			w.place(w.s.Work, "a-1", "a", tc.col)
			w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
			p := Brief(w.s, briefOne("a-1", "new"))
			require.Len(t, p.Refused, 1, tc.name)
			assert.NotContains(t, p.Refused[0].Why, "the machine is RUNNING", tc.name)
			for _, s := range want[tc.name] {
				assert.Contains(t, p.Refused[0].Why, s, tc.name)
			}
		}
	}
}

func TestBriefIsRefusedForASentinelAndAnUnknownCard(t *testing.T) {
	t.Parallel()
	w := editWorld(t)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Brief(w.s, briefOne("a-stop", "new"))
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a-stop is a sentinel, not a primary")
	p = Brief(w.s, briefOne("zz", "new"))
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "no primary zz on the work table")
}

// moveWorld is editWorld with stream b: b-1 ready, and b-2 waiting on a-1, a
// need across the streams.
func moveWorld(t *testing.T) *world {
	t.Helper()
	w := editWorld(t)
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-1"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-2"}, Needs: []string{"a-1"}, Who: "coordinator"}))
	return w
}

// move is a move as the store applies it: held to the lifecycle (Lawful).
func move(w *world, r MoveReq) Plan {
	w.t.Helper()
	r.Who = "coordinator"
	return w.must(Lawful(MoveCards(w.s, r)))
}

func TestMoveTakesUnstartedPrimariesToTheEndOfAnotherStream(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	a2 := *w.s.Work.Placed("a-2")
	move(w, MoveReq{IDs: []string{"a-1", "a-2"}, Stream: "b"})
	a1, a2now := w.s.Work.Placed("a-1"), w.s.Work.Placed("a-2")
	assert.Equal(t, "b", a1.Row)
	assert.Equal(t, "b", a1.F("stream"))
	assert.Equal(t, Ready, a1.Col)
	assert.Equal(t, "b", a2now.Row)
	assert.Equal(t, Waiting, a2now.Col, "a-2 still waits on a-1")
	assert.Equal(t, "a-1", a2now.F("needs"), "a need is by id: it holds")
	assert.Equal(t, a2.Fields["brief"], a2now.F("brief"))
	assert.Equal(t, a2.Fields["admitted"], a2now.F("admitted"))
	for _, id := range []string{"b-1", "b-2"} {
		assert.Less(t, w.s.Work.Placed(id).Score, a1.Score, "moved to the end of b's line, after %s", id)
	}
	assert.Less(t, a1.Score, a2now.Score)
	b2 := w.s.Work.Placed("b-2")
	assert.Equal(t, Waiting, b2.Col)
	assert.Equal(t, "a-1", b2.F("needs"), "a need naming the moved card holds")
	w.clean("after the move")
}

func TestMoveMakesANewStreamAsAddDoes(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "c"})
	assert.True(t, w.s.Work.HasRow("c"))
	assert.True(t, w.s.Merge.HasRow("c"))
	require.NotNil(t, w.s.StreamCtl("c"))
	assert.Equal(t, StreamWaiting, w.s.StreamCtl("c").F("state"))
	assert.Equal(t, "c", w.s.Work.Placed("a-1").Row)
	w.clean("after the move")
}

func TestMovePlacesTheCardsAsAddDoes(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "b", Before: "b-1"})
	assert.Less(t, w.s.Work.Placed("a-1").Score, w.s.Work.Placed("b-1").Score)
	w = moveWorld(t)
	score := 0.25
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "b", Score: &score})
	assert.Equal(t, 0.25, w.s.Work.Placed("a-1").Score)
}

func TestMoveIsRefusedOnARunningMachine(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.s.Running = true
	p := MoveCards(w.s, MoveReq{IDs: []string{"a-1", "a-2"}, Stream: "b", Who: "coordinator"})
	require.Len(t, p.Refused, 2)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "the machine is RUNNING")
	assert.Contains(t, p.Refused[0].Why, "run: nova-sprint stop")
}

func TestMoveIsRefusedForAStartedCard(t *testing.T) {
	t.Parallel()
	for _, tc := range startedCases {
		w := moveWorld(t)
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		p := MoveCards(w.s, MoveReq{IDs: []string{"a-1"}, Stream: "b", Who: "coordinator"})
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, "a-1 is "+tc.col, tc.name)
		assert.Contains(t, p.Refused[0].Why, "keeps its stream", tc.name)
	}
}

func TestMoveIsRefusedIntoItsOwnStreamAndForASentinel(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-stop"}, Sentinel: true, Who: "coordinator"}))
	p := MoveCards(w.s, MoveReq{IDs: []string{"a-1", "a-stop"}, Stream: "a", Who: "coordinator"})
	require.Len(t, p.Refused, 2)
	assert.Contains(t, p.Refused[0].Why, "a-1 is in stream a already")
	assert.Contains(t, p.Refused[1].Why, "a-stop is a sentinel, not a primary")
}

// A ready card the destination would put behind a sentinel is refused by the
// lifecycle: ready -> waiting is only the effect of inserting a sentinel.
func TestMoveBehindASentinelOfAReadyCardIsRefusedByTheLifecycle(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c-1"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Lawful(MoveCards(w.s, MoveReq{IDs: []string{"a-1"}, Stream: "c", Who: "coordinator"}))
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "ready -> waiting only as the effect of inserting a sentinel")
	// in front of the stop it moves, ready
	move(w, MoveReq{IDs: []string{"a-1"}, Stream: "c", Before: "c-stop"})
	assert.Equal(t, Ready, w.s.Work.Placed("a-1").Col)
	assert.Equal(t, "c", w.s.Work.Placed("a-1").Row)
	w.clean("after the move")
}

// A move that would close a cycle of needs through a sentinel is refused,
// as add refuses one: b-2 needs a-1, and behind b's stop a-1 would need it.
func TestMoveThatClosesACycleIsRefused(t *testing.T) {
	t.Parallel()
	w := moveWorld(t)
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Lawful(MoveCards(w.s, MoveReq{IDs: []string{"a-1"}, Stream: "b", Who: "coordinator"}))
	require.Len(t, p.Refused, 1)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "the needs would make a cycle")
}

// A card is re-tiered in any state (the owner, 2026-10-04: "If there are pro cards that
// are really heavy, then let's mark them as heavy"): brief --tier pins the tier on the
// primary as rework --tier does, on a RUNNING machine and for a card dealt, where the
// next attempt draws from it; a card landed, a pinned model, a word that is no class
// and a sentinel are refused, and nothing else of the card changes.
func TestBriefTierRetiersACardInAnyState(t *testing.T) {
	t.Parallel()
	for _, tc := range append([]struct {
		name, col string
		attempt   string
	}{{"ready, never dealt", Ready, "0"}, {"waiting", Waiting, "0"}}, startedCases[:4]...) {
		w := editWorld(t)
		w.s.Running = true
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		before := *w.s.Work.Placed("a-1")
		p := Brief(w.s, tierOne("a-1", "heavy"))
		require.Empty(t, p.Refused, tc.name)
		w.must(p)
		c := w.s.Work.Placed("a-1")
		assert.Equal(t, "heavy", c.F(FieldTier), tc.name)
		assert.Equal(t, "heavy", cardTierOf(c), "%s: the next deal draws from the pinned tier", tc.name)
		assert.Equal(t, "", w.s.NextTier(c), "%s: a pinned tier is its ceiling", tc.name)
		assert.Equal(t, before.F("brief"), c.F("brief"), tc.name)
		assert.Equal(t, before.Col, c.Col, tc.name)
		assert.Equal(t, before.F("attempt"), c.F("attempt"), tc.name)
		assert.Contains(t, p.Units[0].Moved, "a-1 tier pinned to heavy (was -)", tc.name)
	}
	for _, tc := range []struct{ name, id, tier, col, why string }{
		{"landed", "a-1", "heavy", Landed, "a-1 is landed: a card landed keeps its tier"},
		{"no class", "a-1", "ultra", Ready, "--tier wants frontier, heavy, pro or flash, found ultra"},
		{"a sentinel", "a-sentinel", "heavy", Ready, "a-sentinel is a sentinel"},
		{"unknown", "zz-9", "heavy", Ready, "no primary zz-9"},
	} {
		w := editWorld(t)
		w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-sentinel"}, Sentinel: true, Who: "coordinator"}))
		w.place(w.s.Work, "a-1", "a", tc.col)
		p := Brief(w.s, tierOne(tc.id, tc.tier))
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, tc.why, tc.name)
	}
	w := editWorld(t)
	w.must(Brief(w.s, briefOne("a-1", "pinned (s) tier: pro\nmodel: deepseek/deepseek-chat\ntokens: 1000\ndeadline: 600\n\nthe task")))
	p := Brief(w.s, tierOne("a-1", "heavy"))
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "its brief pins model deepseek/deepseek-chat")
	w.must(Brief(w.s, tierOne("a-2", "heavy")))
	p = Brief(w.s, tierOne("a-2", "heavy"))
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "pinned to tier heavy already")
}
