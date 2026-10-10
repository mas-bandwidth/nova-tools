package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSprintCardsViewCoverCardHolder(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Now:   t0,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	s.Work.SetRows([]string{"s1"})

	primary := &Card{ID: "p1", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p1"}}
	s.Work.Put(primary)

	fleet := s.Fleet
	fleet.SetRows([]string{"m1", "friend.f1"})

	fleet.Put(&Card{ID: "m1", Row: "m1", Col: Up, Fields: map[string]string{"status": Up}})
	fleet.Put(&Card{ID: "p1", Row: "m1", Col: Working, Fields: map[string]string{PrimaryField: "p1"}})
	assert.Equal(t, "m1", CardHolder(s, primary))

	fleet.Put(&Card{ID: "p2", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p2"}})
	s.Work.Put(&Card{ID: "p2", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p2"}})
	fleet.Put(&Card{ID: "friend.f1", Row: "friend.f1", Col: Up, Fields: map[string]string{"status": Up}})
	fleet.Put(&Card{ID: "p2", Row: "friend.f1", Col: Working, Fields: map[string]string{PrimaryField: "p2"}})
	assert.Equal(t, "f1", CardHolder(s, &Card{ID: "p2", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p2"}}))

	primary3 := &Card{ID: "p3", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p3"}}
	s.Work.Put(primary3)
	fleet.Put(&Card{ID: "p3", Row: "m1", Col: Review, Fields: map[string]string{PrimaryField: "p3"}})
	assert.Equal(t, "", CardHolder(s, primary3))

	primary4 := &Card{ID: "p4", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p4"}}
	s.Work.Put(primary4)
	assert.Equal(t, "", CardHolder(s, primary4))
}

func TestSprintCardsViewCoverCardRows(t *testing.T) {
	t.Parallel()

	s := &Snapshot{
		Now:   t0,
		Work:  NewTable(Work),
		Fleet: NewTable(Fleet),
	}
	s.Work.SetRows([]string{"s1", "s2"})

	primary1 := &Card{ID: "p1", Row: "s1", Col: Ready, Fields: map[string]string{PrimaryField: "p1", "brief": "flash"}}
	primary2 := &Card{ID: "p2", Row: "s1", Col: Working, Fields: map[string]string{PrimaryField: "p2", "brief": "pro"}}
	primary3 := &Card{ID: "p3", Row: "s2", Col: Review, Fields: map[string]string{PrimaryField: "p3", "brief": "pro"}}
	s.Work.Put(primary1)
	s.Work.Put(primary2)
	s.Work.Put(primary3)

	s.Fleet.SetRows([]string{"m1"})
	s.Fleet.Put(&Card{ID: "p2", Row: "m1", Col: Working, Fields: map[string]string{PrimaryField: "p2"}})

	// All rows
	rows := CardRows(s, CardsFilter{})
	assert.Len(t, rows, 3)
	assert.Equal(t, "p1", rows[0].ID)
	assert.Equal(t, "p2", rows[1].ID)
	assert.Equal(t, "p3", rows[2].ID)

	// Filter by stream
	rows = CardRows(s, CardsFilter{Stream: "s2"})
	assert.Len(t, rows, 1)
	assert.Equal(t, "p3", rows[0].ID)

	// Filter by column
	rows = CardRows(s, CardsFilter{Col: string(Ready)})
	assert.Len(t, rows, 1)
	assert.Equal(t, "p1", rows[0].ID)

	// Filter by holder
	rows = CardRows(s, CardsFilter{Holder: "m1"})
	assert.Len(t, rows, 1)
	assert.Equal(t, "p2", rows[0].ID)

	// Filter by non-existent
	rows = CardRows(s, CardsFilter{Stream: "nonexistent"})
	assert.Len(t, rows, 0)
	assert.NotNil(t, rows)

	rows = CardRows(s, CardsFilter{Col: "nonexistent"})
	assert.Len(t, rows, 0)
	assert.NotNil(t, rows)

	rows = CardRows(s, CardsFilter{Holder: "nonexistent"})
	assert.Len(t, rows, 0)
	assert.NotNil(t, rows)
}

func TestSprintCardsViewCoverCardsBy(t *testing.T) {
	t.Parallel()

	rows := []CardRow{
		{ID: "p1", Stream: "s1", Col: Ready, Tier: "pro", Holder: "m1"},
		{ID: "p2", Stream: "s1", Col: Working, Tier: "pro", Holder: "m1"},
		{ID: "p3", Stream: "s2", Col: Review, Tier: "flash", Holder: ""},
	}

	// By tier
	counts, ok := CardsBy(rows, "tier")
	assert.True(t, ok)
	assert.Equal(t, 2, counts["pro"])
	assert.Equal(t, 1, counts["flash"])

	// By stream
	counts, ok = CardsBy(rows, "stream")
	assert.True(t, ok)
	assert.Equal(t, 2, counts["s1"])
	assert.Equal(t, 1, counts["s2"])

	// By col
	counts, ok = CardsBy(rows, "col")
	assert.True(t, ok)
	assert.Equal(t, 1, counts[string(Ready)])
	assert.Equal(t, 1, counts[string(Working)])
	assert.Equal(t, 1, counts[string(Review)])

	// By holder
	counts, ok = CardsBy(rows, "holder")
	assert.True(t, ok)
	assert.Equal(t, 2, counts["m1"])
	assert.Equal(t, 1, counts["-"])

	// Invalid names
	counts, ok = CardsBy(rows, "")
	assert.False(t, ok)
	assert.Nil(t, counts)

	counts, ok = CardsBy(rows, "invalid")
	assert.False(t, ok)
	assert.Nil(t, counts)
}

func TestSprintCardsViewCoverSortedKeys(t *testing.T) {
	t.Parallel()

	m := map[string]int{"b": 1, "a": 2, "c": 3}
	keys := SortedKeys(m)
	assert.Len(t, keys, 3)
	assert.Equal(t, []string{"a", "b", "c"}, keys)

	empty := map[string]int{}
	keys = SortedKeys(empty)
	assert.Len(t, keys, 0)
}
