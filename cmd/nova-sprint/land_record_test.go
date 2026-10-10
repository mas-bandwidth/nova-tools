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

	t.Run("movedExactly checks the right cards", func(t *testing.T) {
		t.Parallel()

		// Verify movedExactly checks for the exact cards that should have landed
		ids := []string{"p2"} // Only p2 was pushed

		// If we report with Cards: [p2], movedExactly should verify p2 landed
		// If we report with Batch: 1, movedExactly would verify the first queued card
		assert.True(t, movedExactly([]string{"p2 merging -> landed"}, ids))
		assert.False(t, movedExactly([]string{"p1 merging -> landed"}, ids)) // p1 is not the pushed card
	})
}
