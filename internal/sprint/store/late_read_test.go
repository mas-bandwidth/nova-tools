package store

import (
	"math/rand/v2"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The owner, 2026-10-01: "This pesky one card that doesn't clear thing... this is
// a failure mode we must fix. We can't get stuck on the last card." The sprint's
// last card sat in review for minutes on one late read (fleet pass 8, a-036).

// lateReview is a sprint of one primary in review, asked of two readers by the
// machine: the first has read it ok, the second holds it; reader-c is free.
func lateReview(t *testing.T) (*harness, *sprint.Card) {
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	reads := readsAt(h.snap(), h.snap().Work.Card("s1-1"))
	require.Len(t, reads, 2, "asked of two readers")
	require.NotContains(t, []string{reads[0].Row, reads[1].Row}, "reader-c")
	h.must(ReadStep(sprint.ReadReq{As: reads[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{reads[0].ID}}}))
	return h, reads[1]
}

// moreOf is the primary's read cards on reader-c, the one reader lateReview
// left free: the one more reader the tick asks.
func moreOf(h *harness, id string) []*sprint.Card {
	var out []*sprint.Card
	for _, rc := range h.snap().Readers.Of(id) {
		if rc.Row == "reader-c" {
			out = append(out, rc)
		}
	}
	return out
}

// A read out past the late bound gets one more reader, asked by the tick with no
// coordinator verb; the tick right after moves nothing (errata 3 amendment 12);
// the first two ok reads accept the card and the late read is retired.
func TestALateReadIsAskedOfAnotherReaderByTheTick(t *testing.T) {
	t.Parallel()
	h, late := lateReview(t)
	h.must(ReadStep(sprint.ReadReq{As: late.Row, Begin: true, Sel: sprint.Sel{IDs: []string{late.ID}}}))
	h.tick(sprint.LateReadDefault)
	h.machine()
	assert.Empty(t, moreOf(h, "s1-1"), "not late at the bound itself")
	h.tick(time.Second)
	h.machine()
	more := moreOf(h, "s1-1")
	require.Len(t, more, 1, "one more reader asked by the tick")
	assert.Equal(t, sprint.Asked, more[0].Col)
	assert.Empty(t, h.openOf(sprint.NReadLate), "the late read is the machine's, not the coordinator's")
	h.quiet("one more reader asked")
	h.must(ReadStep(sprint.ReadReq{As: more[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{more[0].ID}}}))
	h.machine()
	assert.Equal(t, sprint.Merging, h.state("s1-1"), "accepted on the two oks")
	assert.Nil(t, h.snap().Readers.Placed(late.ID), "the late read is retired at accept")
	h.clean("accepted")
}

// With no reader left to ask, a late read is the coordinator's: "a read card is
// past its deadline" at its deadline, and no reader is asked twice.
func TestALateReadWithNoReaderLeftIsJudged(t *testing.T) {
	t.Parallel()
	h, late := lateReview(t)
	h.tick(sprint.LateReadDefault + time.Second)
	h.machine()
	require.Len(t, moreOf(h, "s1-1"), 1, "reader-c asked as one more")
	h.tick(sprint.DeadlineUnbegun - sprint.LateReadDefault)
	h.machine()
	assert.Len(t, h.snap().Readers.Of("s1-1"), 3, "no reader is left: none is asked twice")
	open := h.openOf(sprint.NReadLate)
	require.Len(t, open, 1, "the read out past its deadline is the coordinator's")
	assert.Equal(t, late.ID, open[0].Note.Card)
}

// A returned read asked again in place, no other reader free, runs on a route
// drawn afresh, never the one it returned on while the tier has another.
func TestAReturnedReadAskedAgainInPlaceRunsOnAnotherRoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "tester"))
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	var back *sprint.Card
	for _, rc := range readsAt(h.snap(), h.snap().Work.Card("s1-1")) {
		if rc.F(sprint.FieldRoute) == "flash-b" {
			back = rc
		}
	}
	// the deal took flash-a, the reads flash-b and flash-a: the index is at flash-b
	require.NotNil(t, back, "a read on flash-b")
	h.must(ReadStep(sprint.ReadReq{As: back.Row, Return: true, Reason: "no verdict (ran=false)", Sel: sprint.Sel{IDs: []string{back.ID}}}))
	h.machine()
	again := h.snap().Readers.Placed(back.ID)
	require.NotNil(t, again, "asked again in place")
	assert.Equal(t, sprint.Asked, again.Col)
	assert.Empty(t, again.F(sprint.FieldReturned))
	assert.Equal(t, "flash-a", again.F(sprint.FieldRoute), "a route drawn afresh, not the one it returned on")
	assert.Equal(t, "prov-flash-a/model-flash-a", again.F(sprint.FieldModel))
	h.clean("asked again in place")
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
