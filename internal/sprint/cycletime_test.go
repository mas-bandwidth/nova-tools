package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stageFlow is one card's waits, in seconds on the injected clock, from its
// ready to its landing (docs/SPEC-SPRINT.md, cycle-time-breakdownb.w1).
type stageFlow struct {
	deal, take, work, readWait, r1, r2, accept, merge int
}

// flowOf drives primary id through every stage on the world's clock, each step
// at the time its wait ends: deal, take, finish, ask, two reads, accept, merge.
func flowOf(w *world, id string, f stageFlow) {
	w.t.Helper()
	sec := func(n int) { w.tick(time.Duration(n) * time.Second) }
	sec(f.deal)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	finishFlow(w, id, f)
}

// finishFlow is flowOf from the deal on: the work card taken and finished, its two
// reads asked together and read one after the other, accepted and merged.
func finishFlow(w *world, id string, f stageFlow) {
	w.t.Helper()
	sec := func(n int) { w.tick(time.Duration(n) * time.Second) }
	card := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	sec(f.take)
	w.must(Take(w.s, TakeReq{As: card.Row, Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	sec(f.work)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	sec(f.readWait)
	w.askReads()
	for i, d := range []int{f.r1, f.r2} {
		sec(d)
		out := readCardsAt(w.s, w.s.Work.Card(id), readAttempt(w.s.Work.Card(id)))
		require.Len(w.t, out, 2-i, "read %d: the reads outstanding", i+1)
		w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: out[0].Row, Verdict: "ok", Sel: Sel{IDs: []string{out[0].ID}}}))
		require.Equal(w.t, Review, w.state(id), "read %d", i+1)
	}
	sec(f.accept)
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{id}}}))
	sec(f.merge)
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	require.Equal(w.t, Landed, w.state(id))
}

// TestStageTimesGiveMedianAndP90PerStage drives three cards through every stage on
// an injected clock and reads the stage times back from the snapshot: the cards
// record their own stamps in the steps that move them (docs/SPEC-SPRINT.md,
// cycle-time-breakdownb.w1), and CycleTimes is the median and p90 of each stage over
// the cards landed in the last day. The third card is reworked once: its rework is
// its finish to its deal again, and its deal wait is no stage of its final attempt.
func TestStageTimesGiveMedianAndP90PerStage(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-1", Brief: proBrief},
		{ID: "s1-2", Brief: proBrief, Needs: []string{"s1-1"}},
		{ID: "s1-3", Brief: proBrief, Needs: []string{"s1-2"}},
	}}))

	flowOf(w, "s1-1", stageFlow{deal: 10, take: 1, work: 100, readWait: 5, r1: 20, r2: 30, accept: 2, merge: 7})
	flowOf(w, "s1-2", stageFlow{deal: 20, take: 2, work: 200, readWait: 6, r1: 21, r2: 31, accept: 3, merge: 8})

	// s1-3: a first attempt, finished, read broken, reworked 49 s after its finish
	sec := func(n int) { w.tick(time.Duration(n) * time.Second) }
	sec(30)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-3"}}}))
	first := w.s.Fleet.Card(w.s.Work.Card("s1-3").F("work"))
	sec(3)
	w.must(Take(w.s, TakeReq{As: first.Row, Sel: Sel{IDs: []string{first.ID}}, Gens: gensOf(w.s, first.ID)}))
	sec(300)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{first.ID}}, Gens: gensOf(w.s, first.ID)}))
	sec(7)
	w.askReads()
	sec(2)
	rc := readCardsAt(w.s, w.s.Work.Card("s1-3"), 1)[0]
	w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "broken", Finding: "cycletime.go:1: off by one, change the count", Sel: Sel{IDs: []string{rc.ID}}}))
	sec(40)
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-3"}}, Fix: "off by one"}))
	finishFlow(w, "s1-3", stageFlow{take: 4, work: 400, readWait: 8, r1: 22, r2: 32, accept: 4, merge: 9})

	got := CycleTimes(w.s, w.s.Now)
	st := func(median, p90 float64, n int) StageStat { return StageStat{Median: median, P90: p90, N: n} }
	want := map[string]StageStat{
		StageNeeds:    st(175, 466, 3), // added together; ready as each need landed: 0, 175 and 466 s
		StageDeal:     st(15, 20, 2),   // ready to first dealt: 10 and 20 s, the reworked card's is its rework
		StageTake:     st(2, 4, 3),
		StageWork:     st(200, 400, 3), // the final attempt's take to its finish
		StageRework:   st(49, 49, 1),   // 7 + 2 + 40 s from its first finish to its deal again
		StageReadWait: st(6, 8, 3),
		StageRead:     st(52, 54, 3), // first ask to the last read
		StageAccept:   st(3, 4, 3),
		StageMerge:    st(8, 9, 3),
	}
	assert.Equal(t, want, got.All)
	assert.Equal(t, map[string]map[string]StageStat{"s1": want}, got.Streams)

	// a card landed over a day ago is no part of the window
	assert.Empty(t, CycleTimes(w.s, w.s.Now.Add(CycleWindow+time.Second)).All)
}
