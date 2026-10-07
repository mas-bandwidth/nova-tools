package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A launch refusal sitting in the failed column moves to withdrawn, and the
// same cards planned again write nothing.
func TestRecountMovesALaunchRefusalOutOfFailed(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{Epoch: 1, Fleet: sprint.NewTable(sprint.Fleet), Work: sprint.NewTable(sprint.Work)}
	s.Fleet.SetRows([]string{"m1"})
	s.Fleet.Put(&sprint.Card{ID: "p.w1", Row: "m1", Col: sprint.DoneFailed, Fields: map[string]string{
		"ok": "no", "report": cardhdr.EndLaunch + ": no worktree",
	}})
	plan, rows := sprint.RecountPlan(s)
	require.False(t, plan.Empty())
	var m1 sprint.RowRecount
	for _, r := range rows {
		if r.Row == "m1" {
			m1 = r
		}
	}
	assert.Equal(t, 1, m1.BeforeFail)
	assert.Equal(t, 0, m1.AfterFail)
	assert.Equal(t, 1, m1.AfterWith)

	c := s.Fleet.Card("p.w1")
	c.Col = sprint.Withdrawn
	c.Fields[sprint.FieldBlame] = sprint.BlameCoordinator
	c.Fields[sprint.FieldDefectClass] = sprint.DefectLaunchRefused
	c.Fields["finding"] = sprint.ExtractFindingFirstLine(c.F("report"))
	s.Fleet.Put(c)
	again, againRows := sprint.RecountPlan(s)
	assert.True(t, again.Empty())
	var after sprint.RowRecount
	for _, r := range againRows {
		if r.Row == "m1" {
			after = r
		}
	}
	assert.Equal(t, 0, after.BeforeFail)
	assert.Equal(t, 0, after.AfterFail)
	assert.Equal(t, 1, after.BeforeWith)
	assert.Equal(t, 1, after.AfterWith)
}
