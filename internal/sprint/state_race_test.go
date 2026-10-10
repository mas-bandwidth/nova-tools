package sprint

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ids is a card list's ids in order, for comparing two reads of one table.
func ids(cards []*Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

// TestConcurrentReadsOfOneTableAreRaceFree pins that one table read by many
// goroutines at once (Cards, Clone, Cell, Column, WithPrefix) does not race on
// the table's lazy caches, and that every reader sees the same id order a
// single reader does. No read warms a cache before the goroutines start: the
// first concurrent reads must build the caches, which is where the race was.
// (docs/SPEC-SPRINT.md section 1, the tables.)
func TestConcurrentReadsOfOneTableAreRaceFree(t *testing.T) {
	t.Parallel()

	tb := NewTable(Work)
	tb.SetRows([]string{"s1", "s2"})
	const totalCards = 100
	wantIDs := make([]string, 0, totalCards)
	for i := 0; i < totalCards; i++ {
		id := fmt.Sprintf("stream-1.%03d", i)
		wantIDs = append(wantIDs, id)
		tb.Put(&Card{
			ID:     id,
			Row:    "s1",
			Col:    Ready,
			Score:  float64(i),
			Fields: map[string]string{PrimaryField: id},
		})
	}
	require.Len(t, wantIDs, totalCards)

	const workers = 8
	const iterations = 50
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < iterations; j++ {
				// assert, not require: Error is safe to call from this
				// goroutine, FailNow (which require calls) is not.
				assert.Equal(t, wantIDs, ids(tb.Cards()), "Cards is every id in order")
				assert.Equal(t, wantIDs, ids(tb.Clone().Cards()), "a clone keeps the order")
				assert.Len(t, tb.Column(Ready), totalCards)
				assert.Len(t, tb.Cell("s1", Ready), totalCards)
				assert.Len(t, tb.WithPrefix("stream-1.0"), totalCards)
				assert.True(t, tb.AnyWithPrefix("stream-1.0"))
				assert.NotNil(t, tb.Card("stream-1.000"))
			}
		}()
	}

	close(start)
	wg.Wait()
}
