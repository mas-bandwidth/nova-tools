package store

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Manual take reads the same half-slot policy as deal (SPEC-SPRINT section 6,
// a read is a consumer card): work costs one slot, a read half, without changing width.
// Seed the backend, then use the actual store step rather than a prefilled Snapshot.
func TestManualFriendTakeUsesTheStoredReadWeight(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cold", "cached twin"} {
		for _, tc := range []struct {
			name               string
			width, work, reads int
			on, fits           bool
		}{
			{"two reads leave one work slot", 2, 0, 2, true, true},
			{"sixteen reads leave eight work slots", 16, 0, 16, true, true},
			{"mixed work and reads fill the last slot", 16, 7, 16, true, true},
			{"half a slot cannot fit work", 16, 8, 15, true, false},
			{"disabled read weighting retains raw cap", 2, 0, 2, false, false},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				const friend = "worker-a"
				row := sprint.FriendRow(friend)
				_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: friend, Width: tc.width, Class: "flash"}})
				require.NoError(t, err)
				h.up(friend)
				require.NoError(t, h.m.RowsAdd(h.ctx, "t-fleet", []string{row}))
				word := "off"
				if tc.on {
					word = sprint.ReadCardsOnWord
				}
				_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(h.m.Revision("t-work")), OperationID: "read-policy", Props: map[string]string{sprint.PropReadCards: word}})
				require.NoError(t, err)
				var members []ntable.BatchMemberEntry
				add := func(id, kind, col string) {
					members = append(members, ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: row, Col: col, Score: 1}, Set: map[string]string{"kind": kind, "gen": "1", "stream": "s1"}})
				}
				for i := range tc.work {
					add(fmt.Sprintf("work-%d.w1", i), "work", sprint.Working)
				}
				for i := range tc.reads {
					add(fmt.Sprintf("read-%d.r1.worker-a", i), "read", sprint.Working)
				}
				add("next.w1", "work", sprint.Ready)
				_, err = h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(h.m.Revision("t-fleet")), OperationID: "working-and-ready", Members: members})
				require.NoError(t, err)
				step := TakeStep(sprint.TakeReq{As: row, Sel: sprint.Sel{IDs: []string{"next.w1"}}, Gens: map[string]int{"next.w1": 1}, Who: row})
				if mode == "cached twin" {
					step.Twin = NewTwin()
					_, _, err = h.st.twinRead(h.ctx, step.Twin, All, nil, nil)
					require.NoError(t, err)
				}
				res := h.run(step)
				if tc.fits {
					assert.Empty(t, res.Refused, "stored half-slot policy must reach TakeStep's snapshot")
					assert.Len(t, res.Moved, 1, "the real store step must admit work within weighted width")
					assert.Equal(t, sprint.Working, h.snap().Fleet.Card("next.w1").Col)
				} else {
					assert.Len(t, res.Refused, 1)
					assert.Empty(t, res.Moved)
					assert.Equal(t, sprint.Ready, h.snap().Fleet.Card("next.w1").Col)
				}
				seats, err := h.st.FriendRows(h.ctx, h.now)
				require.NoError(t, err)
				require.Len(t, seats, 1)
				assert.Equal(t, tc.width, seats[0].Width, "admission never inflates the configured width")
			})
		}
	}
}
