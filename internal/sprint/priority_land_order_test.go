package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// LandOrder applies producer urgency between FIX streams before behind-count,
// then keeps the existing weight and stable input ties (SPEC-SPRINT, Priority).
func TestFixLandOrderAcrossStreamsRetainsProducerUrgency(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, c := range []*Card{
		{ID: "older-fix", Row: "older", Col: Merging, Fields: map[string]string{FieldPriority: PriorityFix, FieldProducerPriority: PriorityNormal, FieldBehind: "50"}},
		{ID: "urgent-fix", Row: "urgent", Col: Merging, Fields: map[string]string{FieldPriority: PriorityFix, FieldProducerPriority: PriorityBlocker, FieldBehind: "1"}},
		{ID: "heavier-fix", Row: "heavier", Col: Merging, Fields: map[string]string{FieldPriority: PriorityFix, FieldProducerPriority: PriorityBlocker, FieldBehind: "2"}},
		{ID: "tied-fix", Row: "tied", Col: Merging, Fields: map[string]string{FieldPriority: PriorityFix, FieldProducerPriority: PriorityBlocker, FieldBehind: "2"}},
		{ID: "ordinary-blocker", Row: "blocker", Col: Merging, Fields: map[string]string{FieldPriority: PriorityBlocker, FieldBehind: "100"}},
	} {
		w.s.Work.Put(c)
	}
	assert.Equal(t, []string{"blocker", "tied", "heavier", "urgent", "older"}, LandOrder(w.s, []string{"older", "urgent", "tied", "heavier", "blocker"}))
}
