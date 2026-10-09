package sprint_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A late report for an attempt that failed finishes it, on the twin store (store.Mem): the
// finish verb loads the failed work card it names and closes the attempt's failed judgment.
func TestALateLandFinishesTheFailedAttemptOnTheStore(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.readCard()
	r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"r-1"}}}))
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card("r-1").F("work"))
	require.NotNil(t, wc)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	gen := r.snap().Fleet.Card(wc.ID).Int("gen")
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}, Failed: true,
		Report: "verdict not-done; the lane died: the runner ended job " + wc.ID + " with no report"}))
	require.Len(t, r.openOnCard(sprint.NWorkFailed, "r-1"), 1)

	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: gen}, Head: readHead, Report: "LAND: done"}))
	s = r.snap()
	pr := s.Work.Card("r-1")
	assert.Equal(t, sprint.Review, pr.Col)
	assert.Equal(t, "ok", pr.F("result"), "the late LAND finishes the attempt")
	assert.Equal(t, readHead, pr.F("head"))
	assert.Equal(t, 1, pr.Int("attempt"), "no second attempt")
	assert.Equal(t, sprint.DoneOK, s.Fleet.Card(wc.ID).Col)
	assert.Empty(t, r.openOnCard(sprint.NWorkFailed, "r-1"), "the failed judgment is closed")
}
