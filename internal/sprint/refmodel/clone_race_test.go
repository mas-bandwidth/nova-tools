package refmodel_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// raceTable is a cold table: a declared row and n cards in one column, whose
// id order no read has built yet.
func raceTable(name, prefix string, n int) *sprint.Table {
	tb := sprint.NewTable(name)
	tb.SetRows([]string{prefix})
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s.%03d", prefix, i)
		tb.Put(&sprint.Card{ID: id, Row: prefix, Col: sprint.Ready, Score: float64(i), Fields: map[string]string{sprint.PrimaryField: id}})
	}
	return tb
}

// TestConcurrentClonesOfOneSnapshotAreRaceFree pins that many goroutines
// cloning one shared snapshot at once do not race on the sprint tables' lazy
// caches, the certification race's exact path
// (refmodel.cloneTable <- Snapshot.Clone <- Table.Cards <- Table.sortedIDs),
// and that every clone holds every card. No read warms a table before the
// goroutines start: the first clones must build the caches.
// (docs/SPEC-SPRINT.md section 1, the tables.)
func TestConcurrentClonesOfOneSnapshotAreRaceFree(t *testing.T) {
	t.Parallel()

	const cards = 100
	base := refmodel.Snapshot{Tables: &sprint.Snapshot{
		Work:    raceTable(sprint.Work, "stream-1", cards),
		Readers: raceTable(sprint.Readers, "reader", cards),
		Merge:   raceTable(sprint.Merge, "merge", cards),
		Fleet:   raceTable(sprint.Fleet, "member", cards),
	}}
	require.NotNil(t, base.Tables)

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
				// assert, not require: Error is safe from this goroutine
				c := base.Clone()
				assert.Len(t, c.Tables.Work.Cards(), cards)
				assert.Len(t, c.Tables.Readers.Cards(), cards)
				assert.Len(t, c.Tables.Merge.Cards(), cards)
				assert.Len(t, c.Tables.Fleet.Cards(), cards)
			}
		}()
	}

	close(start)
	wg.Wait()
}
