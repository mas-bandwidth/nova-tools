package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The selector and the one-step batch plan (select_batch.go) are pure over a
// *Snapshot, so these tests build the snapshot with the package's world
// harness and the plan helpers by hand: no store, clock, subprocess or
// network.

// Given says the selector names anything: a batch verb with none is refused.
func TestSprintSelectBatchCoverSelectorGiven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		q    Selector
		want bool
	}{
		{"the zero selector names nothing", Selector{}, false},
		{"Sentinels alone is no selection", Selector{Sentinels: true}, false},
		{"a stream", Selector{Stream: "s1"}, true},
		{"a who", Selector{Who: WhoNone}, true},
		{"a state", Selector{State: Ready}, true},
		{"ids", Selector{IDs: []string{"s1-1"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, c.q.Given())
		})
	}
}

// Words is the selector as the command line gives it, for the step's lines.
func TestSprintSelectBatchCoverWords(t *testing.T) {
	t.Parallel()
	q := Selector{Stream: "s1", Who: "friend.f1", State: Ready, IDs: []string{"a", "b"}}
	assert.Equal(t, "--stream s1 --who friend.f1 --state ready --ids-file (2 ids)", q.Words())
	assert.Equal(t, "", Selector{}.Words())
}

// Given says the brief transform changes anything.
func TestSprintSelectBatchCoverBriefEditGiven(t *testing.T) {
	t.Parallel()
	assert.False(t, BriefEdit{}.Given())
	assert.True(t, BriefEdit{SetBase: "dev"}.Given())
	assert.True(t, BriefEdit{DropWho: true}.Given())
}

// NoneSelected is why a selector that selects no card changes nothing.
func TestSprintSelectBatchCoverNoneSelected(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "the selector names no card (--stream s1); nothing was changed", NoneSelected(Selector{Stream: "s1"}))
}

// Selected is the ids of the cards the selector holds of, in work order
// (stream, then score, then id), and a refusal for each --ids-file id that is
// no placed card or that the rest of the selector does not hold of.
func TestSprintSelectBatchCoverSelected(t *testing.T) {
	t.Parallel()
	// A snapshot per subtest: the tables cache on first read, so parallel
	// subtests must not share one (the race rule).
	snapshot := func(t *testing.T) *Snapshot {
		s := newWorld(t).s
		s.Work.SetRows([]string{"s1", "s2"})
		s.Work.Put(&Card{ID: "b", Row: "s1", Col: Ready, Score: 2, Rev: 1, Fields: map[string]string{}})
		s.Work.Put(&Card{ID: "a", Row: "s1", Col: Ready, Score: 1, Rev: 1, Fields: map[string]string{}})
		s.Work.Put(&Card{ID: "c", Row: "s2", Col: Ready, Score: 0, Rev: 1, Fields: map[string]string{}})
		s.Work.Put(&Card{ID: "x", Row: "s2", Col: Ready, Score: 5, Rev: 1, Fields: map[string]string{}})
		return s
	}

	t.Run("the order is stream, then score, then id", func(t *testing.T) {
		t.Parallel()
		ids, refused := Selected(snapshot(t), Selector{})
		assert.Equal(t, []string{"a", "b", "c", "x"}, ids)
		assert.Empty(t, refused)
	})
	t.Run("an --ids-file id is refused when named twice, unplaced, or not held by the rest", func(t *testing.T) {
		t.Parallel()
		q := Selector{Stream: "s1", IDs: []string{"a", "a", "nope", "x"}}
		ids, refused := Selected(snapshot(t), q)
		assert.Equal(t, []string{"a"}, ids)
		assert.Equal(t, []Refusal{
			{"a", "named twice in --ids-file"},
			{"nope", noSuchCard},
			{"x", "the rest of the selector (" + q.Words() + ") does not hold of it"},
		}, refused)
	})
}

// Batch plans its steps in order on the state the plans before them leave,
// folds their changes into one entry per card, and is all or none.
func TestSprintSelectBatchCoverBatch(t *testing.T) {
	t.Parallel()
	// A snapshot per subtest: the tables cache on first read, so parallel
	// subtests must not share one (the race rule).
	snapshot := func(t *testing.T) *Snapshot {
		s := newWorld(t).s
		s.Work.SetRows([]string{"s1", "s2"})
		s.Work.Put(&Card{ID: "s1-1", Row: "s1", Col: Ready, Score: 1, Rev: 1, Fields: map[string]string{"stage": "zero"}})
		return s
	}

	t.Run("a later step is planned on the state the steps before it leave", func(t *testing.T) {
		t.Parallel()
		seen := ""
		p := Batch(snapshot(t), []func(*Snapshot) Plan{
			func(cur *Snapshot) Plan {
				c := cur.Work.Card("s1-1")
				return Plan{Units: []Unit{{Key: c.ID, Changes: []Change{change(Work, setEntry(c, map[string]string{"stage": "one"}))}}}}
			},
			func(cur *Snapshot) Plan {
				seen = cur.Work.Card("s1-1").F("stage")
				return Plan{}
			},
		})
		assert.Empty(t, p.Refused)
		assert.Equal(t, "one", seen)
	})

	t.Run("a refusal of any step refuses the whole plan with every step's refusals", func(t *testing.T) {
		t.Parallel()
		p := Batch(snapshot(t), []func(*Snapshot) Plan{
			func(*Snapshot) Plan { return Plan{Refused: []Refusal{{"a", "one"}}} },
			func(*Snapshot) Plan { return Plan{Refused: []Refusal{{"b", "two"}}} },
		})
		assert.Equal(t, []Refusal{{"a", "one"}, {"b", "two"}}, p.Refused)
		assert.Empty(t, p.Units)
	})

	t.Run("a property written twice keeps one write with the last value", func(t *testing.T) {
		t.Parallel()
		p := Batch(snapshot(t), []func(*Snapshot) Plan{
			func(*Snapshot) Plan { return Plan{Props: []PropWrite{{Table: Readers, Name: "hint", Value: "one"}}} },
			func(*Snapshot) Plan { return Plan{Props: []PropWrite{{Table: Readers, Name: "hint", Value: "two"}}} },
		})
		require.Len(t, p.Props, 1)
		assert.Equal(t, "two", p.Props[0].Value)
	})

	t.Run("a row added twice is one row", func(t *testing.T) {
		t.Parallel()
		p := Batch(snapshot(t), []func(*Snapshot) Plan{
			func(*Snapshot) Plan { return Plan{Rows: []RowAdd{{Table: Work, Row: "s2"}}} },
			func(*Snapshot) Plan { return Plan{Rows: []RowAdd{{Table: Work, Row: "s2"}}} },
		})
		assert.Equal(t, []RowAdd{{Table: Work, Row: "s2"}}, p.Rows)
	})

	t.Run("two creates of one card are refused under the key batch", func(t *testing.T) {
		t.Parallel()
		p := Batch(snapshot(t), []func(*Snapshot) Plan{
			func(*Snapshot) Plan {
				return Plan{Units: []Unit{{Key: "n", Changes: []Change{change(Work, createEntry("n", "s1", Ready, 1, nil))}}}}
			},
			func(*Snapshot) Plan {
				return Plan{Units: []Unit{{Key: "n", Changes: []Change{change(Work, createEntry("n", "s1", Ready, 1, nil))}}}}
			},
		})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "batch", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "create it")
	})
}

// applyPlan is the snapshot a plan leaves: its bumps added, its properties
// written, its changes applied to the tables it names.
func TestSprintSelectBatchCoverApplyPlan(t *testing.T) {
	t.Parallel()
	s := newWorld(t).s
	s.Fleet.SetRows([]string{"m1"})
	s.Fleet.Put(&Card{ID: "m1", Row: "m1", Col: Up, Rev: 1, Fields: map[string]string{DoneOK: "3"}})
	applyPlan(s, Plan{
		Units: []Unit{
			{Bumps: []Bump{{Table: Fleet, ID: "m1", Field: DoneOK, Delta: 2}}},
			{Changes: []Change{change("nowhere", createEntry("ghost", "s1", Ready, 1, nil))}},
		},
		Props: []PropWrite{{Table: Readers, Name: "hint", Value: "two"}},
	})
	assert.Equal(t, "5", s.Fleet.Card("m1").F(DoneOK), "a bump adds Delta to the value the plan read")
	v, ok := s.Readers.Prop("hint")
	assert.True(t, ok)
	assert.Equal(t, "two", v)
	assert.Nil(t, s.Work.Card("ghost"), "a change of an unknown table is ignored")
}

// applyEntry is one table entry applied as the store's twin applies it.
func TestSprintSelectBatchCoverApplyEntry(t *testing.T) {
	t.Parallel()
	t.Run("a create places a new card", func(t *testing.T) {
		t.Parallel()
		tb := NewTable(Work)
		applyEntry(tb, createEntry("n", "s1", Ready, 3, map[string]string{"k": "v"}))
		c := tb.Card("n")
		require.NotNil(t, c)
		assert.Equal(t, "s1", c.Row)
		assert.Equal(t, Ready, c.Col)
		assert.Equal(t, 3.0, c.Score)
		assert.Equal(t, "v", c.F("k"))
	})
	t.Run("a move without a score keeps the old score; with a score it takes it", func(t *testing.T) {
		t.Parallel()
		tb := NewTable(Work)
		tb.SetRows([]string{"s1", "s2"})
		tb.Put(&Card{ID: "c", Row: "s1", Col: Ready, Score: 1, Rev: 1, Fields: map[string]string{"k": "v"}})
		applyEntry(tb, moveEntry(tb.Card("c"), "s2", Working, nil))
		assert.Equal(t, "s2", tb.Card("c").Row)
		assert.Equal(t, Working, tb.Card("c").Col)
		assert.Equal(t, 1.0, tb.Card("c").Score)
		score := 7.0
		applyEntry(tb, ntable.BatchMemberEntry{ID: "c", Move: &ntable.MemberMoveOp{Row: "s1", Col: Review, Score: &score}})
		assert.Equal(t, "s1", tb.Card("c").Row)
		assert.Equal(t, Review, tb.Card("c").Col)
		assert.Equal(t, 7.0, tb.Card("c").Score)
	})
	t.Run("a removal takes the card off the table and an unset takes the field", func(t *testing.T) {
		t.Parallel()
		tb := NewTable(Work)
		tb.Put(&Card{ID: "c", Row: "s1", Col: Ready, Score: 1, Rev: 1, Fields: map[string]string{}})
		applyEntry(tb, removeEntry(tb.Card("c"), nil))
		assert.Empty(t, tb.Card("c").Col)
		assert.False(t, tb.Card("c").Placed())
		tb.Put(&Card{ID: "d", Row: "s1", Col: Ready, Score: 1, Rev: 1, Fields: map[string]string{"k": "v"}})
		applyEntry(tb, setEntry(tb.Card("d"), nil, "k"))
		assert.False(t, tb.Card("d").Has("k"))
	})
	t.Run("a pure guard entry is not put", func(t *testing.T) {
		t.Parallel()
		tb := NewTable(Work)
		applyEntry(tb, guardEntry(&Card{ID: "ghost", Row: "s1", Col: Ready, Rev: 1}))
		assert.Nil(t, tb.Card("ghost"))
	})
}

// RecutSel is recut by selector: the selection made on the snapshot, and each
// card re-cut as its twin in one step, all or none.
func TestSprintSelectBatchCoverRecutSel(t *testing.T) {
	t.Parallel()

	t.Run("a refused selection passes through", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := RecutSel(w.s, RecutSelReq{Sel: Selector{IDs: []string{"nope"}}})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "nope", p.Refused[0].Key)
		assert.Equal(t, noSuchCard, p.Refused[0].Why)
		assert.Empty(t, p.Said)
	})
	t.Run("an empty selection is refused under the selector key", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := RecutSel(w.s, RecutSelReq{Sel: Selector{Stream: "s9"}})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "selector", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "names no card")
	})
	t.Run("DropWho on a brief with no WHO line and no tier refuses its twin", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := RecutSel(w.s, RecutSelReq{Sel: Selector{Stream: "s1"}, Edit: BriefEdit{DropWho: true}})
		require.Len(t, p.Refused, 2)
		assert.Contains(t, p.Refused[0].Why, "its twin would change nothing")
	})
	t.Run("SetBase plans every card and says one line each and the total", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := RecutSel(w.s, RecutSelReq{Sel: Selector{Stream: "s1"}, Edit: BriefEdit{SetBase: "dev"}})
		assert.Empty(t, p.Refused)
		require.NotEmpty(t, p.Said)
		assert.Equal(t, "recut selected=2 by --stream s1", p.Said[len(p.Said)-1])
		assert.Contains(t, p.Said, "recut s1-1: selected")
		assert.Contains(t, p.Said, "recut s1-2: selected")
	})
}

// BriefSel is brief by selector: each selected card's own brief with the
// transform made, and re-tiered with a tier, in one step.
func TestSprintSelectBatchCoverBriefSel(t *testing.T) {
	t.Parallel()

	t.Run("a refused selection passes through", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := BriefSel(w.s, BriefSelReq{Sel: Selector{IDs: []string{"nope"}}})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "nope", p.Refused[0].Key)
		assert.Equal(t, noSuchCard, p.Refused[0].Why)
		assert.Empty(t, p.Said)
	})
	t.Run("an empty selection is refused under the selector key", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := BriefSel(w.s, BriefSelReq{Sel: Selector{Stream: "s9"}})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "selector", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "names no card")
	})
	t.Run("DropWho on a brief with no WHO line and no tier leaves the brief of each card", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := BriefSel(w.s, BriefSelReq{Sel: Selector{Stream: "s1"}, Edit: BriefEdit{DropWho: true}})
		require.Len(t, p.Refused, 2)
		assert.Contains(t, p.Refused[0].Why, "leaves the brief of")
	})
	t.Run("a tier with an unchanged brief passes every card over without a refusal", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := BriefSel(w.s, BriefSelReq{Sel: Selector{Stream: "s1"}, Tier: "pro", Edit: BriefEdit{DropWho: true}})
		assert.Empty(t, p.Refused)
	})
}

// SelectIDs is a step over named ids made a step over a selection: the
// selection made on the snapshot is handed to the plan as the ids it names.
func TestSprintSelectBatchCoverSelectIDs(t *testing.T) {
	t.Parallel()

	t.Run("the ids are handed in work order and the plan is said per card and total", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		var got []string
		p := SelectIDs(w.s, "rework", Selector{Stream: "s1"}, func(ids []string) Plan {
			got = ids
			return Plan{}
		})
		assert.Equal(t, []string{"s1-1", "s1-2"}, got)
		assert.Empty(t, p.Refused)
		require.NotEmpty(t, p.Said)
		assert.Equal(t, "rework selected=2 by --stream s1", p.Said[len(p.Said)-1])
	})
	t.Run("a refused plan is returned unchanged with no selected lines", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := SelectIDs(w.s, "rework", Selector{Stream: "s1"}, func(ids []string) Plan {
			return Plan{Refused: []Refusal{{ids[0], "no"}}}
		})
		assert.Equal(t, []Refusal{{"s1-1", "no"}}, p.Refused)
		assert.Empty(t, p.Said)
	})
	t.Run("an empty selection is refused under the selector key", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := SelectIDs(w.s, "rework", Selector{Stream: "s9"}, func([]string) Plan { return Plan{} })
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "selector", p.Refused[0].Key)
	})
}
