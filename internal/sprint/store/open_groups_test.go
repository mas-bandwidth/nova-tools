package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// OpenGroups is the inbox's judgment groups from the open notes alone (ops.go): the ids and
// members Inbox computes, with no notifications, stream clocks, weights or tables read. It is
// what rework and drop ask of one card, whether the inbox holds a group of several naming it.
func TestOpenGroupsAreTheInboxsJudgmentGroupsFromTheOpenNotesAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", true, "")
	h.finishAttempt("s1-2", true, "")
	groups, err := h.st.OpenGroups(h.ctx)
	require.NoError(t, err)
	v, err := h.st.Inbox(h.ctx, sprint.DeadlineJudgment, 0, 1000)
	require.NoError(t, err)
	members := func(gs []sprint.Group) map[string][]string {
		out := map[string][]string{}
		for _, g := range gs {
			if g.Kind == sprint.Judgment && len(g.Notes) > 0 { // a stored judgment, not the machine's read-time line
				out[g.ID] = g.Members
			}
		}
		return out
	}
	want := members(v.Groups)
	require.NotEmpty(t, want)
	assert.Equal(t, want, members(groups), "the same groups by id and members")
	failed := h.openOf(sprint.NWorkFailed)
	require.Len(t, failed, 2)
	g, ok := sprint.FindGroup(groups, failed[0].Note.ID)
	require.True(t, ok, "the group is named by its oldest note")
	assert.Equal(t, []string{"s1-1", "s1-2"}, g.Members)
}
