package sprintdash

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviewSplit reads the review_reason off where --json --rows' rows and sets the copy's
// review_reads and review_defect (docs/SPEC-SPRINT.md, "The review reason: reads and
// defect"): a review card whose reason is defect is the seat's work, every other review
// card a read, and a card not in review counts for neither.
func TestReviewSplitCountsDefectAndReads(t *testing.T) {
	t.Parallel()
	row := func(id, col, reason string) map[string]any {
		fields := map[string]string{}
		if reason != "" {
			fields["review_reason"] = reason
		}
		return map[string]any{"id": id, "column": col, "fields": fields}
	}
	body := map[string]any{
		"landed": 0,
		"rows": []any{
			row("s1-1", "review", "defect"),
			row("s1-2", "review", "read"),
			row("s1-3", "review", ""),
			row("s1-4", "working", "defect"),
		},
	}
	b, err := json.Marshal(body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(reviewSplit(b), &out))
	assert.InDelta(t, 2, out["review_reads"], 0, "two review cards wait on a read")
	assert.InDelta(t, 1, out["review_defect"], 0, "one review card is a brief defect")
}

// A copy with no rows is left byte for byte: the split is counted from the cards, and no
// cards is no split.
func TestReviewSplitLeavesACopyWithoutRows(t *testing.T) {
	t.Parallel()
	body := json.RawMessage(`{"landed":0,"all":5,"tables":{"work":{"s1":{"review":"1"}}}}`)
	assert.Equal(t, string(body), string(reviewSplit(body)))
}
