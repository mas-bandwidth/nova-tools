package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The brief-defect judgment stamps blame on the primary. A work card that
// still has no blame takes the same stamp; one the finish already classed
// keeps that class.
func TestABriefDefectJudgmentStampsCoordinatorBlame(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	finding := "a.go:12 drops the error from Close; return it"
	brokenOnce(t, w, finding)
	rules(w, on())
	brokenOnce(t, w, finding)
	rules(w, on())
	pr := w.s.Work.Card("s1-1")
	assert.Equal(t, BlameCoordinator, pr.F(FieldBlame))
	assert.Equal(t, DefectBrief, pr.F(FieldDefectClass))
	if wc := w.s.Fleet.Placed(pr.F("work")); wc != nil {
		assert.NotEmpty(t, wc.F(FieldBlame))
	}
}
