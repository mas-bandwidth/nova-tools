package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFixPriorityOrdersDealAskAndMerge(t *testing.T) {
	t.Parallel()
	w := setup(t, 7)
	levels := []string{PriorityLow, PriorityNormal, PriorityReader, PriorityHigh, PriorityFix, PriorityCritical, PriorityBlocker}
	var cards []*Card
	for i, level := range levels {
		c := w.s.Work.Card("s1-" + itoa(i+1))
		c.Fields[FieldPriority] = level
		cards = append(cards, c)
	}
	want := []*Card{cards[6], cards[5], cards[4], cards[3], cards[2], cards[1], cards[0]}
	assert.Equal(t, want, ladderOrder(cards))
	assert.Equal(t, []*Card{cards[6], cards[5], cards[4], cards[3], cards[0], cards[1], cards[2]}, readOrder(cards))
	assert.Equal(t, want, MergePriorityOrder(w.s, cards))
	assert.Equal(t, PriorityFix, ReadPriority(cards[4]))
	fields := map[string]string{}
	priorityOnRead(fields, cards[4])
	assert.Equal(t, PriorityFix, fields[FieldPriority])
	level, why := PriorityOfBrief("tier: pro\nPRIORITY: fix\n\nRepair the failure.")
	assert.Empty(t, why)
	assert.Equal(t, PriorityFix, level)
}

func TestFixMergePriorityPreservesDependencies(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	a, b, c := w.s.Work.Card("s1-1"), w.s.Work.Card("s1-2"), w.s.Work.Card("s1-3")
	b.Fields[FieldPriority], b.Fields["needs"] = PriorityFix, a.ID
	c.Fields[FieldPriority] = PriorityHigh
	assert.Equal(t, []*Card{c, a, b}, MergePriorityOrder(w.s, []*Card{a, b, c}))
	assert.Equal(t, []string{a.ID}, WaitsFor(w.s, b, nil), "ordering does not mutate the snapshot")
}

func TestReworkPriorityPolicyOnNextAttempt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, level, policy, want string
		rule                      bool
	}{
		{"failed rule", "", "", PriorityFix, true},
		{"seat rework", "", "", PriorityFix, false},
		{"low", PriorityLow, "", PriorityFix, false},
		{"high stays high", PriorityHigh, "", PriorityHigh, true},
		{"critical stays critical", PriorityCritical, "", PriorityCritical, false},
		{"blocker stays blocker", PriorityBlocker, "", PriorityBlocker, false},
		{"fix stays fix", PriorityFix, PriorityHigh, PriorityFix, false},
		{"high policy", "", PriorityHigh, PriorityHigh, true},
		{"keep normal", "", ReworkKeep, PriorityNormal, true},
		{"keep low", PriorityLow, ReworkKeep, PriorityLow, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := heldFor(t, "the generated output is incorrect")
			pr := w.s.Work.Card("s1-1")
			if tc.level != "" {
				pr.Fields[FieldPriority] = tc.level
			}
			if tc.policy != "" {
				w.s.Work.SetProps(map[string]string{PropReworkPriority: tc.policy})
			}
			if tc.rule {
				p, _ := TickRuleRework(w.s, on())
				require.NotEmpty(t, p.Units)
				w.must(p)
			} else {
				w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{pr.ID}}, Who: "coordinator"}))
			}
			pr = w.s.Work.Card(pr.ID)
			got, _ := CardPriority(pr)
			assert.Equal(t, tc.want, got)
			if pr.Col == Working {
				wc := w.s.Fleet.Card(pr.F("work"))
				require.NotNil(t, wc)
				assert.Equal(t, tc.want, QueuePriority(wc), "immediate deal carries the new level")
			}
		})
	}
}

func TestReworkPrioritySetting(t *testing.T) {
	t.Parallel()
	for _, value := range []string{PriorityFix, PriorityHigh, ReworkKeep, "urgent"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			s := settingsSnapshot(nil)
			p := Set(s, SetReq{ReworkPriority: value, Who: "coord"})
			if value == "urgent" {
				require.NotEmpty(t, p.Refused)
				assert.Empty(t, p.Props)
				assert.Contains(t, p.Refused[0].Why, "fix, high or keep")
			} else {
				require.Empty(t, p.Refused)
				require.Len(t, p.Props, 1)
				assert.Equal(t, PropReworkPriority, p.Props[0].Name)
				assert.Equal(t, value, p.Props[0].Value)
			}
		})
	}
}

func TestCorrectedBriefDefectGetsFixPriority(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "the generated output is incorrect")
	pr := w.s.Work.Card("s1-1")
	pr.Fields[FieldBriefDefect] = BriefDefectPaths
	w.must(Brief(w.s, BriefReq{ID: pr.ID, Brief: pr.F("brief") + "\nThe allowed paths include the missing implementation.\n", Who: "coordinator"}))
	pr = w.s.Work.Card(pr.ID)
	assert.Equal(t, Ready, pr.Col)
	assert.Equal(t, PriorityFix, pr.F(FieldPriority))
	assert.Empty(t, pr.F(FieldBriefDefect), "the corrected brief can be reworked normally")
}

func TestFixPriorityFlowsThroughMergeAndConflict(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.s.Work.Card("s1-2").Fields[FieldPriority] = PriorityHigh
	w.s.Work.Card("s1-3").Fields[FieldPriority] = PriorityFix
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	assert.Equal(t, Landed, w.s.Work.Card("s1-3").Col)
	assert.Equal(t, Merging, w.s.Work.Card("s1-1").Col)
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Cards: []string{"s1-1"}, Conflict: "s1-1", ConflictKind: "file", Note: "the head h1 of s1-1 does not merge: CONFLICT (content): Merge conflict in internal/x.go"}))
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, Ready, pr.Col)
	assert.Equal(t, PriorityFix, pr.F(FieldPriority))
}

func TestBrokenReadGetsFixPriority(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	brokenOnce(t, w, "internal/x.go:12 returns the wrong output; return the calculated value")
	p, _ := TickRuleRework(w.s, on())
	require.NotEmpty(t, p.Units)
	w.must(p)
	assert.Equal(t, PriorityFix, w.s.Work.Card("s1-1").F(FieldPriority))
}

func TestFixMergeNeverPassesAnUnmetDependency(t *testing.T) {
	t.Parallel()
	t.Run("a newly added dependency outside the merge queue", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 3)
		accepted(w, "s1-1", "s1-3")
		stuck := w.s.Merge.Card("s1-3")
		stuck.Col = Stuck
		w.s.Merge.Put(stuck)
		pr := w.s.Work.Card("s1-1")
		pr.Fields[FieldPriority], pr.Fields["needs"] = PriorityFix, "s1-2"
		assert.Empty(t, MergePriorityOrder(w.s, w.s.Merge.Cell("s1", Queued)))
		p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1})
		require.NotEmpty(t, p.Refused)
		assert.Contains(t, p.Refused[0].Why, "prerequisites")
		assert.Empty(t, p.Units)
	})
	t.Run("a named batch omitting its queued predecessor", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		accepted(w, "s1-1", "s1-2")
		pr := w.s.Work.Card("s1-2")
		pr.Fields[FieldPriority], pr.Fields["needs"] = PriorityFix, "s1-1"
		p := MergeStep(w.s, MergeReq{Stream: "s1", Cards: []string{pr.ID}})
		require.NotEmpty(t, p.Refused)
		assert.Contains(t, p.Refused[0].Why, "omits prerequisites s1-1")
		assert.Empty(t, p.Units)
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Cards: []string{"s1-2", "s1-1"}}))
		assert.Equal(t, Landed, w.s.Work.Card(pr.ID).Col)
	})
}

func TestFixWorkingCountsArePublished(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.s.Work.Card("s1-1").Fields[FieldPriority] = PriorityFix
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	wc := w.s.Fleet.Card("s1-1.w1")
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	rows, _ := RowCardCounts(w.s)
	visible := map[string]int{}
	for _, field := range RowCardFields {
		visible[field] = rows[wc.Row][field]
	}
	assert.Equal(t, 1, visible["fix_working"], "the row fields published by where retain a manually set fix")
}

func TestFixPrioritySurvivesRedoAndSubsequentRead(t *testing.T) {
	t.Parallel()
	w := stoppedForConflict(t)
	w.must(Redo(w.s, RedoReq{Sel: Sel{IDs: []string{"s1-2"}}, Who: "coordinator"}))
	pr := w.s.Work.Card("s1-2")
	assert.Equal(t, PriorityFix, pr.F(FieldPriority))
	wc := w.s.Fleet.Card(pr.F("work"))
	require.NotNil(t, wc)
	assert.Equal(t, PriorityFix, QueuePriority(wc))
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Report: "the conflict is resolved"}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{pr.ID}}}))
	reads := readsAt(w.s, w.s.Work.Card(pr.ID), pr.Int("attempt"))
	require.NotEmpty(t, reads)
	for _, rc := range reads {
		assert.Equal(t, PriorityFix, QueuePriority(rc))
	}
}

func TestFixStateSeparatesReworkFromReadReview(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	first := w.s.Work.Card("s1-1")
	first.Fields[FieldPriority] = PriorityFix
	other := w.s.Work.Card("s1-2")
	other.Fields[FieldPriority] = PriorityCritical
	assert.Equal(t, map[string]map[string]int{"s1": {Ready: 1}}, FixStateCounts(w.s))
	// A completed repair awaiting an independent read belongs in review.
	w.place(w.s.Work, first.ID, first.Row, Review)
	assert.Empty(t, FixStateCounts(w.s))
	first.Fields["work"] = first.ID + ".w1"
	w.s.Fleet.Put(&Card{ID: first.F("work"), Row: "m1", Col: DoneFailed, Fields: map[string]string{"kind": "work", "primary": first.ID}})
	assert.Equal(t, map[string]map[string]int{"s1": {Review: 1}}, FixStateCounts(w.s))
	first.Fields[FieldPriority] = PriorityHigh
	assert.Equal(t, map[string]map[string]int{"s1": {Review: 1}}, FixStateCounts(w.s), "failed high work awaits repair too")
	first.Fields[FieldPriority] = PriorityBlocker
	assert.Empty(t, FixStateCounts(w.s), "blockers keep their urgent colour")
}

func TestFixStateIncludesACompletedBriefDefect(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealStarted(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"})
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "HOLD: brief defect: the TEST line names a test the PATHS cannot reach"}))
	require.Equal(t, DoneDefect, w.s.Fleet.Card("s1-1.w1").Col)
	require.Equal(t, Review, w.s.Primary("s1-1").Col)
	assert.Equal(t, map[string]map[string]int{"s1": {Review: 1}}, FixStateCounts(w.s))
}
