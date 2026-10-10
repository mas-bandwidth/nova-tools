package sprint

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConcurrentReadsOfOneTableAreRaceFree verifies that concurrent readers
// calling Cards and Clone on one table do not race on internal lazy caches.
func TestConcurrentReadsOfOneTableAreRaceFree(t *testing.T) {
	t.Parallel()

	tb := NewTable(Work)
	tb.SetRows([]string{"s1", "s2"})
	const totalCards = 100
	for i := 0; i < totalCards; i++ {
		id := fmt.Sprintf("stream-1.%03d", i)
		tb.Put(&Card{
			ID:     id,
			Row:    "s1",
			Col:    Ready,
			Score:  float64(i),
			Fields: map[string]string{PrimaryField: id},
		})
	}

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
				cards := tb.Cards()
				require.Len(t, cards, totalCards)

				cloned := tb.Clone()
				require.Len(t, cloned.Cards(), totalCards)

				col := tb.Column(Ready)
				require.Len(t, col, totalCards)

				cell := tb.Cell("s1", Ready)
				require.Len(t, cell, totalCards)

				pre := tb.WithPrefix("stream-1.0")
				require.NotEmpty(t, pre)

				hasPre := tb.AnyWithPrefix("stream-1.0")
				require.True(t, hasPre)
			}
		}()
	}

	close(start)
	wg.Wait()
}
