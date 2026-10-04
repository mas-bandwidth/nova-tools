package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStepsReviewCoverNamedExtras pins NamedExtras (steps_review.go): the named
// cards a step must read as records when they are not on the table. It names
// the ids the table does not hold, under the table's logical name, in the order
// they were named, and returns nil when every named card is on the table. Every
// case builds its Snapshot and Table by hand: no store, no clock, no subprocess,
// no sleep.
func TestStepsReviewCoverNamedExtras(t *testing.T) {
	t.Parallel()
	table := func(name string, cards ...*Card) *Table {
		tbl := NewTable(name)
		for _, c := range cards {
			tbl.Put(c)
		}
		return tbl
	}
	placed := func(id string) *Card {
		return &Card{ID: id, Row: "s1", Col: Asked, Rev: 1, Fields: map[string]string{}}
	}
	unplaced := func(id string) *Card {
		return &Card{ID: id, Row: "s1", Col: "", Rev: 1, Fields: map[string]string{}}
	}
	cases := []struct {
		name  string
		s     *Snapshot
		table string
		ids   []string
		want  map[string][]string
	}{
		{
			"every named card on the table is no extra",
			&Snapshot{Readers: table(Readers, placed("s1.r1.a"), placed("s1.r1.b"))},
			Readers, []string{"s1.r1.a", "s1.r1.b"},
			nil,
		},
		{
			"a named card the table does not hold is an extra under the table's name",
			&Snapshot{Readers: table(Readers, placed("s1.r1.a"))},
			Readers, []string{"s1.r1.a", "s1.r1.b"},
			map[string][]string{Readers: {"s1.r1.b"}},
		},
		{
			"the extras keep the order the ids were named in",
			&Snapshot{Fleet: table(Fleet, placed("m1"))},
			Fleet, []string{"m3", "m2", "m1"},
			map[string][]string{Fleet: {"m3", "m2"}},
		},
		{
			"a card kept but not placed is an extra all the same",
			&Snapshot{Work: table(Work, unplaced("s1-1"))},
			Work, []string{"s1-1"},
			map[string][]string{Work: {"s1-1"}},
		},
		{
			"a table the snapshot did not load names every id",
			&Snapshot{},
			Merge, []string{"s2", "s1"},
			map[string][]string{Merge: {"s2", "s1"}},
		},
		{
			"no ids named is no extra",
			&Snapshot{},
			Work, nil,
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			extras := NamedExtras(c.table, c.ids)
			assert.Equal(t, c.want, extras(c.s))
		})
	}
}
