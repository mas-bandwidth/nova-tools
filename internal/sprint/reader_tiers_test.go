package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReaderTiersStoresTheLadderAndRefusesARepeat(t *testing.T) {
	t.Parallel()
	got, err := ParseReaderTiers("pro, flash")
	require.NoError(t, err)
	assert.Equal(t, "flash,pro", got)
	got, err = ParseReaderTiers("all")
	require.NoError(t, err)
	assert.Equal(t, "flash,pro,heavy,frontier", got, "all names every tier")
	got, err = ParseReaderTiers("default")
	require.NoError(t, err)
	assert.Equal(t, "", got, "default is the empty cell: flash on a fleet reader, every tier on a friend's")
	_, err = ParseReaderTiers("flash,flash")
	assert.Error(t, err)
	_, err = ParseReaderTiers("nope")
	assert.Error(t, err)
}

// The ask never asks a reader a read outside the tiers its row names. An empty
// tiers cell is every tier with no route in the store (a fleet reader's is flash with one). A pro card with one reader of its tier up raises
// the existing few-readers judgment and nothing is asked of a flash reader.
func TestTheAskNeverAsksAReaderOutsideItsTiers(t *testing.T) {
	t.Parallel()

	both := newTierWorld(t, ReaderUp, ReaderUp)
	flash, pro := both.s.Work.Card("flash-1"), both.s.Work.Card("pro-1")
	assert.ElementsMatch(t, []string{"reader-flash", "reader-all", "reader-all2"}, both.s.freeReaders(flash, 1),
		"a flash card may be asked of a flash reader or of a reader of every tier")
	assert.ElementsMatch(t, []string{"reader-all", "reader-all2"}, both.s.freeReaders(pro, 1),
		"a pro card is asked only of readers whose tiers include pro")
	plan, _ := TickAsk(both.s, TickReq{})
	both.must(plan)
	assert.Contains(t, []string{"reader-flash", "reader-all", "reader-all2"}, askedReader(t, both, "flash-1"))
	assert.NotEqual(t, "reader-flash", askedReader(t, both, "pro-1"), "a pro read is not asked of the flash reader")
	assert.Contains(t, []string{"reader-all", "reader-all2"}, askedReader(t, both, "pro-1"))

	one := newTierWorld(t, ReaderUp, ReaderAway)
	plan, _ = TickAsk(one.s, TickReq{})
	var few bool
	for _, n := range plan.Notes {
		if n.Type == NFewReaders {
			few = true
		}
	}
	assert.True(t, few, "one reader of the pro tier up raises the few-readers judgment: %+v", plan.Notes)
	one.must(plan)
	assert.Nil(t, one.s.Readers.Placed(ReadCardID("pro-1", 1, "reader-flash")), "nothing is asked of the flash reader")
	assert.Empty(t, readsAt(one.s, one.s.Work.Card("pro-1"), 1), "the pro card is not asked")
	assert.NotEmpty(t, readsAt(one.s, one.s.Work.Card("flash-1"), 1), "the flash card is still asked")

	back := newTierWorld(t, ReaderUp, ReaderUp)
	back.s.Readers.Put(&Card{ID: ReadCardID("pro-1", 1, "reader-flash"), Row: "reader-flash", Col: Asked, Rev: 1,
		Fields: map[string]string{"kind": "read", "primary": "pro-1", "reader": "reader-flash", "attempt": "1", "stream": "s1", FieldReturned: "t"}})
	plan, _ = TickAsk(back.s, TickReq{})
	back.must(plan)
	old := back.s.Readers.Card(ReadCardID("pro-1", 1, "reader-flash"))
	require.NotNil(t, old)
	assert.False(t, old.Placed(), "a returned read is not asked again in place of a reader outside the tier")
	assert.Equal(t, "returned", old.F("retired_by"))
	assert.NotEqual(t, "reader-flash", askedReader(t, back, "pro-1"))

	level := newWorld(t, "reader-flash", "reader-all")
	level.s.Readers.Texts = map[string]map[string]string{"reader-flash": {"tiers": "flash"}}
	level.s.ReaderStates = map[string]string{"reader-flash": ReaderUp, "reader-all": ReaderUp}
	level.s.Work.SetRows([]string{"s1"})
	level.s.Work.Put(&Card{ID: "pro-1", Row: "s1", Col: Review, Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1", "brief": proBrief}})
	level.s.Readers.Put(&Card{ID: ReadCardID("pro-1", 1, "reader-all"), Row: "reader-all", Col: Asked, Rev: 1,
		Fields: map[string]string{"kind": "read", "primary": "pro-1", "reader": "reader-all", "attempt": "1", "stream": "s1"}})
	moved, _ := TickLevelReads(level.s, TickReq{})
	assert.Empty(t, moved.Units, "the level does not ask a pro read of a flash reader: %+v", moved.Units)
}

// newTierWorld is three readers up or away as named, one flash card and one pro
// card in review. reader-flash reads flash only; the other two read every tier.
func newTierWorld(t *testing.T, all, all2 string) *world {
	t.Helper()
	w := newWorld(t, "reader-flash", "reader-all", "reader-all2")
	w.s.Readers.Texts = map[string]map[string]string{"reader-flash": {"tiers": "flash"}}
	w.s.ReaderStates = map[string]string{"reader-flash": ReaderUp, "reader-all": all, "reader-all2": all2}
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(Add(w.s, AddReq{Brief: "tier: flash", Stream: "s1", IDs: []string{"flash-1"}}))
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s1", IDs: []string{"pro-1"}}))
	toReview(w, "flash-1", "pro-1")
	return w
}

func askedReader(t *testing.T, w *world, id string) string {
	t.Helper()
	got := readerNames(readsAt(w.s, w.s.Work.Card(id), 1))
	require.Len(t, got, 1, "%s reads: %v", id, got)
	return got[0]
}
