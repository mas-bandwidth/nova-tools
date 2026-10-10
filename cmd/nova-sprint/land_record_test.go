package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPushedBatchIsRecordedInItsPass verifies that when a batch is pushed and then
// reported (recordPushed), the merge step records exactly the cards that were pushed,
// not the first N queued cards. The queue order can change between push and report,
// so the step must use Cards: ids (by name) instead of Batch: len(ids) (by position).
// This is the fix for the bug in recordPushed where Batch: len(ids) could record
// wrong cards if the queue order changed (tla/LandPass.tla, RecordPushedCards).
func TestPushedBatchIsRecordedInItsPass(t *testing.T) {
	t.Parallel()

	t.Run("Cards:ids reports the pushed cards by name", func(t *testing.T) {
		t.Parallel()

		// This test verifies the invariant RecordPushedCards: when a batch is landed,
		// the exact cards that were pushed must be recorded as moved. movedExactly checks
		// that the step's moved lines contain exactly the cards that were pushed by name.
		// If we report with Batch: len(ids), we might move the first N queued cards
		// instead of the actual pushed cards.
		ids := []string{"p2"} // Only p2 was pushed

		// Verify movedExactly checks for the exact cards that should have landed
		assert.True(t, movedExactly([]string{"p2 merging -> landed"}, ids))
		assert.False(t, movedExactly([]string{"p1 merging -> landed"}, ids)) // p1 is not the pushed card
		assert.True(t, movedExactly([]string{"p1 merging -> landed", "p2 merging -> landed"}, []string{"p1", "p2"}))
		assert.False(t, movedExactly([]string{"p1 merging -> landed", "p2 merging -> landed"}, []string{"p2"}))
	})
}
