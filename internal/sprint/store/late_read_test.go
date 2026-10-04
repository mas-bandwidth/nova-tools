package store

import (
	"math/rand/v2"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The owner, 2026-10-01: "This pesky one card that doesn't clear thing... this is
// a failure mode we must fix. We can't get stuck on the last card." The sprint's
// last card sat in review for minutes on one late read (fleet pass 8, a-036):
// its returned read went back to the route it failed on, and the level moved
// its other read from reader to reader.

// A returned read asked again in place, no other reader free, runs on a route
// drawn afresh, never the one it returned on while the tier has another.
func TestAReturnedReadAskedAgainInPlaceRunsOnAnotherRoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"))
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "tester"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"}) // a pro card on pro (flash first: escalated)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	var back *sprint.Card
	for _, rc := range readsAt(h.snap(), h.snap().Work.Card("s1-1")) {
		if rc.F(sprint.FieldRoute) == "pro-b" {
			back = rc
		}
	}
	// the deal took pro-a, the reads pro-b and pro-a: the index is at pro-b
	require.NotNil(t, back, "a read on pro-b")
	h.must(ReadStep(sprint.ReadReq{As: back.Row, Return: true, Reason: "no verdict (ran=false)", Sel: sprint.Sel{IDs: []string{back.ID}}}))
	h.machine()
	again := h.snap().Readers.Placed(back.ID)
	require.NotNil(t, again, "asked again in place")
	assert.Equal(t, sprint.Asked, again.Col)
	assert.Empty(t, again.F(sprint.FieldReturned))
	assert.Equal(t, "pro-a", again.F(sprint.FieldRoute), "a route drawn afresh, not the one it returned on")
	assert.Equal(t, "prov-pro-a/model-pro-a", again.F(sprint.FieldModel))
	h.clean("asked again in place")
}

// A returned read taken to another free reader runs on a route drawn afresh,
// never the one it returned on while the tier has another: the same exclusion
// as a read asked again in place.
func TestAReturnedReadTakenToAnotherReaderRunsOnAnotherRoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"))
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "tester"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"}) // a pro card on pro (flash first: escalated)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	var back *sprint.Card
	for _, rc := range readsAt(h.snap(), h.snap().Work.Card("s1-1")) {
		if rc.F(sprint.FieldRoute) == "pro-b" {
			back = rc
		}
	}
	// the deal took pro-a, the reads pro-b and pro-a: the index is at pro-b
	require.NotNil(t, back, "a read on pro-b")
	h.must(ReadStep(sprint.ReadReq{As: back.Row, Return: true, Reason: "no verdict (ran=false)", Sel: sprint.Sel{IDs: []string{back.ID}}}))
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", false, "tester"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.machine()
	assert.Nil(t, h.snap().Readers.Placed(back.ID), "the returned read is taken back")
	moved := h.snap().Readers.Placed(sprint.ReadCardID("s1-1", 1, "reader-c"))
	require.NotNil(t, moved, "asked of the free reader")
	assert.Equal(t, "pro-a", moved.F(sprint.FieldRoute), "a route drawn afresh, not the one it returned on")
	h.clean("taken to another reader")
}

// levelMove is one move of the readers' level: the card it left and the card
// it asked.
var levelMove = regexp.MustCompile(`^(\S+) \S+:asked -> \S+:asked \((\S+)\)$`)

// Twenty ticks with the readers' loads shifting under them (new work every
// third tick; each reader at random begins a read, reports what it reads, or
// waits): the level moves a read at most once, so a late read is not asked
// afresh on reader after reader. Before the rule this run moved two reads twice.
func TestTheLevelMovesAReadAtMostOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-d"}))
	h.setup(4)
	h.startMachine()
	h.machine()
	rng := rand.New(rand.NewPCG(4, 7))
	readers := []string{"reader-a", "reader-b", "reader-c", "reader-d"}
	made := map[string]bool{} // read cards a level move asked
	moves := 0
	for i := 0; i < 20; i++ {
		if i%3 == 0 {
			h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
		}
		h.work("m1")
		h.work("m2")
		for _, p := range h.machine().Parts {
			if p.Name != sprint.PartLevelReads {
				continue
			}
			for _, m := range p.Moved {
				x := levelMove.FindStringSubmatch(m)
				require.NotNil(t, x, "a level move: %s", m)
				assert.False(t, made[x[1]], "tick %d moved %s, itself asked by a level move", i, x[1])
				made[x[2]] = true
				moves++
			}
		}
		s := h.snap()
		for _, rd := range readers {
			switch rng.IntN(3) {
			case 0:
				if asked := s.Readers.Cell(rd, sprint.Asked); len(asked) > 0 {
					h.must(ReadStep(sprint.ReadReq{As: rd, Begin: true, Sel: sprint.Sel{IDs: []string{asked[0].ID}}}))
				}
			case 1:
				for _, rc := range s.Readers.Cell(rd, sprint.Reading) {
					h.must(ReadStep(sprint.ReadReq{As: rd, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
				}
			}
		}
	}
	assert.Positive(t, moves, "the loads shifted: the level moved reads")
	h.clean("twenty ticks")
}
