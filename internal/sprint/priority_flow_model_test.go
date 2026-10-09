package sprint

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPriorityFlowSourceBridge replays actual TickDeal creation receipts through
// tla/PriorityFlowSource.tla. It compares source observations, not two copies of
// the ranking function. The TLA replay independently checks eligible competitors
// and shared room; a reversed source receipt remains a model violation.
func TestPriorityFlowSourceBridge(t *testing.T) {
	t.Parallel()
	w, seat := priorityWorld(t, 1, 1, 0, 0)
	seat.Width = 4 // DealAhead*4 less the existing work card leaves seven places.
	ids := []string{"l", "n", "h", "f", "c", "b"}
	levels := []string{PriorityLow, PriorityNormal, PriorityHigh, PriorityFix, PriorityCritical, PriorityBlocker}
	var cards []CardAdd
	for i, id := range ids {
		cards = append(cards, CardAdd{ID: id, Brief: "c: work\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy\nPRIORITY: " + levels[i] + "\n\nThe task."})
	}
	w.must(Add(w.s, AddReq{Stream: "s2", Cards: cards}))
	p, _ := TickDeal(w.s, TickReq{Friends: []FriendSeat{seat}})
	var observed []string
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table != Fleet || ch.Entry.Create == nil || ch.Entry.Create.Row != FriendRow(seat.Name) {
				continue
			}
			fields := ch.Entry.Set
			id := fields["primary"]
			if id == "s1-1" {
				id = "r" // The source read's primary is the model's reader-level job.
			}
			observed = append(observed, id)
		}
	}
	require.Len(t, observed, 7)
	data, err := os.ReadFile("../../tla/PriorityFlowSource.tla")
	require.NoError(t, err)
	line := "SourceObserved == <<\"" + strings.Join(observed, "\", \"") + "\">>"
	assert.Contains(t, string(data), line, "refresh the source observation and rerun the TLC replay when receipts change")
	w.must(p)
	assert.Equal(t, 8, friendLoad(w.s, seat.Name), "the source uses one shared room for seven admissions plus the existing work")
	for _, c := range w.s.Fleet.Column(Ready, Working) {
		if c.F("kind") == "read" {
			assert.Equal(t, PriorityReader, QueuePriority(c))
		}
	}
}

// The source's Add/SetPriority/Rework are the bridge for PriorityDefaults and the
// read->finding->fix->review lifecycle model; blocker reclaim has no source bridge
// because it remains an unimplemented target.
func TestPriorityFlowAdmissionAndFixBridge(t *testing.T) {
	t.Parallel()
	t.Run("default and header precedence", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.must(SetPriority(w.s, PriorityReq{Stream: "s1", Level: PriorityHigh, Reason: "urgent stream", Who: "coordinator"}))
		assert.Equal(t, PriorityHigh, w.s.Work.Card("s1-1").F(FieldPriority), "setting a stream also sets its existing cards")
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "inherit", Brief: "c: inherited\n\nThe task."}, {ID: "override", Brief: "c: override\nPRIORITY: low\n\nThe task."}}}))
		assert.Equal(t, PriorityHigh, w.s.Work.Card("inherit").F(FieldPriority))
		assert.Equal(t, PriorityLow, w.s.Work.Card("override").F(FieldPriority))
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{{ID: "plain", Brief: "c: normal\n\nThe task."}}}))
		got, _ := CardPriority(w.s.Work.Card("plain"))
		assert.Equal(t, PriorityNormal, got)
	})
	t.Run("finding opens elevated next attempt", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		finished(w, "s1-1", false)
		brokenOnce(t, w, "line 1: retain the missing value")
		before := readAttempt(w.s.Work.Card("s1-1"))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}))
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, before+1, readAttempt(pr))
		assert.Equal(t, PriorityFix, pr.F(FieldPriority))
		assert.Equal(t, PriorityReader, ReadPriority(pr))
	})
	t.Run("priority survives findings review and merge", func(t *testing.T) {
		t.Parallel()
		for _, initial := range []string{PriorityNormal, PriorityHigh, PriorityCritical, PriorityBlocker} {
			t.Run(initial, func(t *testing.T) {
				t.Parallel()
				w := setup(t, 1)
				w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: initial, Reason: "priority flow", Who: "coordinator"}))
				finished(w, "s1-1", false)
				brokenOnce(t, w, "line 1: retain the missing value")
				w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "retain the missing value", Who: "coordinator"}))
				want := PriorityFix
				pr := w.s.Work.Card("s1-1")
				wc := w.s.Fleet.Card(pr.F("work"))
				require.NotNil(t, wc)
				assert.Equal(t, want, QueuePriority(wc))
				assert.Equal(t, initial, wc.F("producer_priority"), "fix keeps originating urgency separately")
				w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
				w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
				w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
				for _, rc := range w.s.Readers.Of("s1-1") {
					assert.Equal(t, PriorityReader, QueuePriority(rc), "subsequent reads retain their reader role")
					assert.Equal(t, initial, rc.F("producer_priority"), "read retains originating urgency separately")
				}
				readOK(w, "s1-1")
				w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
				assert.Equal(t, Merging, w.s.Work.Card("s1-1").Col)
				assert.Equal(t, want, w.s.Work.Card("s1-1").F(FieldPriority))
				w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
				assert.Equal(t, Landed, w.s.Work.Card("s1-1").Col)
				assert.Equal(t, want, w.s.Work.Card("s1-1").F(FieldPriority))
			})
		}
	})
	t.Run("fixed merge peers retain producer urgency", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		accepted(w, "s1-1", "s1-2")
		w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1", "s1-2"}, Level: PriorityFix, Reason: "fix role", Who: "coordinator"}))
		w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityBlocker, Reason: "urgent fix producer", Who: "coordinator"}))
		assert.Equal(t, PriorityFix, w.s.Work.Card("s1-2").F(FieldPriority))
		assert.Equal(t, PriorityBlocker, w.s.Work.Card("s1-2").F("producer_priority"))
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
		assert.Equal(t, Landed, w.s.Work.Card("s1-2").Col, "the later FIX with urgent origin lands before the earlier FIX")
		assert.Equal(t, Merging, w.s.Work.Card("s1-1").Col)
	})
	t.Run("urgent merge respects dependency", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 3)
		accepted(w, "s1-1", "s1-2", "s1-3")
		w.s.Work.Card("s1-2").Fields[FieldPriority] = PriorityBlocker
		w.s.Work.Card("s1-2").Fields["needs"] = "s1-1"
		w.s.Work.Card("s1-3").Fields[FieldPriority] = PriorityCritical
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
		assert.Equal(t, Landed, w.s.Work.Card("s1-3").Col, "eligible critical precedes normal; blocked blocker remains queued")
		assert.Equal(t, Merging, w.s.Work.Card("s1-2").Col)
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2}))
		assert.Equal(t, Landed, w.s.Work.Card("s1-1").Col)
		assert.Equal(t, Landed, w.s.Work.Card("s1-2").Col)
	})
}
