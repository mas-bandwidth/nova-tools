package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A selector holds of a card when every field given holds of it (docs/SPEC-SPRINT.md,
// "One selector, one step"): a hard pin is the friend's too, a landed card never.
func TestASelectorHoldsWhenEveryFieldHolds(t *testing.T) {
	t.Parallel()
	amy := &Card{ID: "a", Row: "s1", Col: Ready, Fields: map[string]string{FieldWho: "friend.amy"}}
	only := &Card{ID: "o", Row: "s1", Col: Waiting, Fields: map[string]string{FieldWho: "only.friend.amy"}}
	none := &Card{ID: "n", Row: "s2", Col: Waiting, Fields: map[string]string{FieldHeld: "t"}}
	landed := &Card{ID: "l", Row: "s1", Col: Landed, Fields: map[string]string{FieldWho: "friend.amy"}}
	for _, tc := range []struct {
		q    Selector
		want []*Card
	}{
		{Selector{Who: "friend.amy"}, []*Card{amy, only}},
		{Selector{Who: "amy"}, []*Card{amy, only}},
		{Selector{Who: WhoNone}, []*Card{none}},
		{Selector{Who: "friend.amy", State: "waiting"}, []*Card{only}},
		{Selector{State: "held"}, []*Card{none}},
		{Selector{State: "waiting"}, []*Card{only}},
		{Selector{Stream: "s1", IDs: []string{"a", "n"}}, []*Card{amy}},
	} {
		var got []*Card
		for _, c := range []*Card{amy, only, none, landed} {
			if tc.q.Selects(c) {
				got = append(got, c)
			}
		}
		assert.Equal(t, tc.want, got, "%+v", tc.q)
	}
	assert.Contains(t, Selector{Who: "friend.a b"}.Check(), "--who wants")
	assert.Contains(t, Selector{State: "done"}.Check(), "--state wants")
	assert.Empty(t, Selector{Who: "friend", State: "merging"}.Check())
}

// A brief transform edits the header block alone: the WHO line taken out, the BASE line
// replaced or put under line 1; prose below the header is never touched.
func TestABriefTransformEditsTheHeaderAlone(t *testing.T) {
	t.Parallel()
	brief := "c1: a card\nREPO: r\nWHO: friend amy\nBASE: main\n\nWHO: this prose stays\nBASE: so does this"
	assert.Equal(t, "c1: a card\nREPO: r\nBASE: main\n\nWHO: this prose stays\nBASE: so does this", BriefEdit{DropWho: true}.Apply(brief))
	assert.Equal(t, "c1: a card\nREPO: r\nWHO: friend amy\nBASE: v2\n\nWHO: this prose stays\nBASE: so does this", BriefEdit{SetBase: "v2"}.Apply(brief))
	assert.Equal(t, "c1: a card\nBASE: v2\nREPO: r\n\nprose", BriefEdit{SetBase: "v2"}.Apply("c1: a card\nREPO: r\n\nprose"))
}

// The batch folds a card's later changes into its first, guarded on what the step read: a
// set then a removal is one removal with both; a card changed after it left the table, or
// created twice, is refused.
func TestABatchFoldsOneEntryPerCard(t *testing.T) {
	t.Parallel()
	c := &Card{ID: "x", Row: "s1", Col: Waiting, Rev: 7, Fields: map[string]string{"needs": "a"}}
	units, bad := foldUnits([]Unit{
		{Key: "x", Changes: []Change{change(Work, setEntry(c, map[string]string{"needs": "ab"}))}, Moved: "x needs a -> ab"},
		{Key: "x", Changes: []Change{change(Work, removeEntry(withField(c, "rev", "8"), map[string]string{"outcome": "dropped"}))}, Moved: "x dropped"},
	})
	assert.Empty(t, bad)
	if assert.Len(t, units, 1) {
		e := units[0].Changes[0].Entry
		assert.True(t, e.Remove)
		assert.Equal(t, "7", e.Expect.Revision, "guarded on the revision the step read")
		assert.Equal(t, map[string]string{"needs": "ab", "outcome": "dropped"}, e.Set)
		assert.Equal(t, "x needs a -> ab; x dropped", units[0].Moved)
	}
	_, bad = foldUnits([]Unit{
		{Changes: []Change{change(Work, removeEntry(c, nil))}},
		{Changes: []Change{change(Work, setEntry(c, map[string]string{"k": "v"}))}},
	})
	assert.Contains(t, bad, "after it left the table")
	create := createEntry("n", "s1", Waiting, 1, nil)
	_, bad = foldUnits([]Unit{{Changes: []Change{change(Work, create)}}, {Changes: []Change{change(Work, create)}}})
	assert.Contains(t, bad, "create it")
}
