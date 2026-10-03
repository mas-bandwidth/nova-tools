//go:build functional

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// whereWallMax is the wall time one where --json may take at 3,000 cards on the
// in-memory store: the requirement is under 1 s on the live store
// (docs/SPEC-SPRINT.md section 1); the store's own exchanges add to this.
const whereWallMax = 200 * time.Millisecond

// where --json at 3,000 cards (bigSprint) completes inside whereWallMax of real
// time once the tick has counted the where record. Real time is this tier's:
// the unit tier asserts the round trips (TestWhereReadsTheTableNotEveryCardAtThreeThousandCards).
func TestWhereAtThreeThousandCardsIsInsideTheWallBound(t *testing.T) {
	t.Parallel()
	ta := bigSprint(t)
	began := time.Now()
	byCards, _ := ta.whereJSON()
	cards := time.Since(began)
	ta.ok("tick")
	began = time.Now()
	counted, _ := ta.whereJSON()
	took := time.Since(began)
	t.Logf("where --json at 3,000 cards: %s from the where record, %s reading the cards before it", took, cards)
	require.Equal(t, byCards.Summary, counted.Summary)
	require.Less(t, took, whereWallMax, "where --json took %s, the bound is %s", took, whereWallMax)
}
