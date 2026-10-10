package sprint

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// readsTable is a table of cards over four rows and every state, a sentinel
// in each row, each card a primary's, with no index built yet.
func readsTable() *Table {
	t := NewTable(Work)
	t.SetRows([]string{"s0", "s1", "s2", "s3"})
	for i := range 240 {
		row := fmt.Sprintf("s%d", i%4)
		c := &Card{ID: fmt.Sprintf("c%03d", i), Row: row, Col: string(States[i%len(States)]), Score: float64(240 - i),
			Fields: map[string]string{PrimaryField: fmt.Sprintf("p%d", i%7)}}
		if i%31 == 0 {
			c.Fields["kind"] = "sentinel"
		}
		t.Put(c)
	}
	t.Put(&Card{ID: "kept", Fields: map[string]string{}}) // no place: a kept record
	return t
}

// tableReads is everything the table's reads say, as text: every read that
// builds an index on first use.
func tableReads(t *Table) string {
	out := fmt.Sprint(idsOf(t.Cards()), idsOf(t.WithPrefix("c1")), t.AnyWithPrefix("c2"), t.AnyWithPrefix("zz"))
	for _, row := range t.Rows() {
		out += fmt.Sprint(idsOf(t.Column(string(Ready), string(Waiting))), idsOf(t.Cell(row, string(Working))), t.Count(row, string(Review)))
		line, stops := t.lineStops(row)
		out += fmt.Sprint(idsOf(line), stops, idsOf(t.openLine(row)))
	}
	for p := range 7 {
		out += fmt.Sprint(idsOf(t.Of(fmt.Sprintf("p%d", p))))
	}
	return out + fmt.Sprint(idsOf(t.Frozen().Cards()))
}

func idsOf(cs []*Card) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// A table is read by any number of goroutines at once: the indexes its reads
// build on first use (the ids in order, the cells, the lines and their stops)
// are built once, and no read writes what another is reading. Run under -race,
// as CI runs it: two goroutines building one index at once is a race it names.
func TestOneTableIsReadByManyGoroutinesAtOnce(t *testing.T) {
	t.Parallel()
	want := tableReads(readsTable())
	for range 20 {
		shared := readsTable()
		got := make([]string, 8)
		var wg sync.WaitGroup
		for g := range got {
			wg.Go(func() { got[g] = tableReads(shared) })
		}
		wg.Wait()
		for g, s := range got {
			require.Equal(t, want, s, "goroutine %d read the shared table differently from a table read alone", g)
		}
	}
}

// A change after reads is seen by the next read: Put and Drop keep the ids in
// order, and the cells and lines are built again.
func TestAReadAfterAChangeSeesTheChange(t *testing.T) {
	t.Parallel()
	tb := readsTable()
	before := tableReads(tb)
	tb.Put(&Card{ID: "c100x", Row: "s1", Col: string(Ready), Score: 0.5, Fields: map[string]string{PrimaryField: "p1"}})
	tb.Drop("c001")
	after := tableReads(tb)
	require.NotEqual(t, before, after)
	fresh := NewTable(Work)
	fresh.SetRows(tb.Rows())
	for _, c := range tb.Cards() {
		fresh.Put(c)
	}
	require.Equal(t, tableReads(fresh), after, "a table changed after its reads reads as one built with the change")
}
