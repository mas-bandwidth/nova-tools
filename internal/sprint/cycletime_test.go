package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stageWorld is a sprint of one stream with one up member and two readers, and the
// clock the test moves by hand.
func stageWorld(t *testing.T) *world {
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	return w
}

// through takes a card from its add to its landing, waiting n seconds more at every
// stage than the one before (stage k waits k*n seconds, k from 1; the first read lands halfway
// through the reads' wait, the second at its end).
func through(w *world, id string, n int) {
	sec := func(k int) { w.tick(time.Duration(k*n) * time.Second) }
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", IDs: []string{id}}))
	sec(1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	wc := w.s.Work.Card(id).F("work")
	sec(2)
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
	sec(3)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
	sec(4)
	w.must(Ask(w.s, AskReq{}))
	reads := readsAt(w.s, w.s.Work.Card(id), 1)
	w.tick(time.Duration(5*n) * time.Second / 2)
	w.must(Read(w.s, ReadReq{As: reads[0].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{reads[0].ID}}}))
	w.tick(time.Duration(5*n) * time.Second / 2)
	w.must(Read(w.s, ReadReq{As: reads[1].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{reads[1].ID}}}))
	sec(6)
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{id}}}))
	sec(7)
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
}

// Wall-clock lens (docs/SPEC-SPRINT.md, the cycle-time-breakdown subsection): three
// cards through every stage on an injected clock give each stage's median and p90, by
// stream and overall.
func TestStageTimesGiveMedianAndP90PerStage(t *testing.T) {
	t.Parallel()
	w := stageWorld(t)
	for i, id := range []string{"s1-1", "s1-2", "s1-3"} {
		through(w, id, 10*(i+1))
	}
	got := StageTimesOf(w.s, w.s.Now)
	// stage k took k*n seconds for n = 10, 20, 30: the median is the n = 20 case, the p90 of
	// three (nearest rank) the 30 case. Add to ready is no wait: a card with no need is
	// ready as it is added.
	want := map[string]StageStat{
		"add_to_ready":      {0, 0, 3},
		"ready_to_dealt":    {20, 30, 3},
		"dealt_to_taken":    {40, 60, 3},
		"taken_to_finished": {60, 90, 3},
		"finished_to_asked": {80, 120, 3},
		"asked_to_read":     {100, 150, 3},
		"read_to_accepted":  {120, 180, 3},
		"queued_to_landed":  {140, 210, 3},
	}
	require.Equal(t, want, got.Overall, "overall: %+v", got.Overall)
	require.Equal(t, want, got.Streams["s1"], "stream s1: %+v", got.Streams)
	require.NotContains(t, got.Overall, "rework", "no card was reworked")
}

// A landing older than a day is not in the window; a rework is the time from a finish
// to the next deal.
func TestStageTimesWindowAndRework(t *testing.T) {
	t.Parallel()
	w := stageWorld(t)
	through(w, "s1-1", 10)
	w.tick(25 * time.Hour)
	require.Empty(t, StageTimesOf(w.s, w.s.Now).Overall, "a card landed 25 h ago is outside the window")

	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", IDs: []string{"s1-2"}}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	wc := w.s.Work.Card("s1-2").F("work")
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
	w.must(Ask(w.s, AskReq{}))
	reads := readsAt(w.s, w.s.Work.Card("s1-2"), 1)
	w.must(Read(w.s, ReadReq{As: reads[0].F("reader"), Verdict: "broken", Finding: "line 1: off by one", Sel: Sel{IDs: []string{reads[0].ID}}}))
	w.tick(90 * time.Second)
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "off by one"}))
	wc = w.s.Work.Card("s1-2").F("work")
	require.Equal(t, "s1-2.w2", wc)
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
	w.must(Ask(w.s, AskReq{}))
	for _, rc := range readsAt(w.s, w.s.Work.Card("s1-2"), 2) {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
	got := StageTimesOf(w.s, w.s.Now)
	require.Equal(t, StageStat{90, 90, 1}, got.Overall["rework"], "rework: %+v", got.Overall)
	require.Equal(t, 1, got.Overall["add_to_ready"].N, "only the card landed now is in the window: %+v", got.Overall)
}
