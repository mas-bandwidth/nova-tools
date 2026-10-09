package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type friendFleetProbe struct {
	*Mem
	observe *friendFleetObservation
}
type friendFleetObservation struct {
	shapes     []ntable.Table
	ids        []string
	reads      int
	shapeReads int
	fault      string
}

func (p *friendFleetProbe) AtEpoch(epoch uint64, old bool) Backend {
	return &friendFleetProbe{Mem: p.Mem.AtEpoch(epoch, old).(*Mem), observe: p.observe}
}
func (p *friendFleetProbe) Shapes(ctx context.Context, names []string) ([]ntable.Table, error) {
	shapes, err := p.Mem.Shapes(ctx, names)
	p.observe.shapeReads++
	if err == nil && len(shapes) > 0 && p.observe.fault == "cleared" {
		shapes[0].Epoch++
	}
	return shapes, err
}
func (p *friendFleetProbe) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	p.observe.shapes = append(p.observe.shapes, shapes...)
	return p.Mem.CellIDs(ctx, shapes)
}
func (p *friendFleetProbe) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	o := p.observe
	o.ids = append(o.ids, ids...)
	o.reads++
	res, err := p.Mem.ReadSet(ctx, table, ids)
	if err == nil && o.reads == 1 {
		switch o.fault {
		case "revision":
			res.Revision++
		case "missing":
			res.Missing = append(res.Missing, ids[0])
		case "unplaced":
			res.Members[0].Placed = false
		case "error":
			err = errors.New("record read failed")
		}
	}
	return res, err
}

// fillFriendUsage keeps mixed machine/control and four friend lifecycle cells in
// the real Mem backend. The full Load supplies the independent totals oracle.
func fillFriendUsage(t *testing.T, h *harness, machines int) []FriendRow {
	t.Helper()
	rows := []FriendRow{{Name: "alpha"}, {Name: "beta"}}
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-fleet", []string{"m1", "friend.alpha", "friend.beta", "control"}))
	shapes, err := h.m.Shapes(h.ctx, []string{"t-fleet"})
	require.NoError(t, err)
	members := make([]ntable.BatchMemberEntry, 0, machines+9)
	add := func(id, row, col string) {
		members = append(members, ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: row, Col: col}, Set: map[string]string{sprint.FieldUsage: "input=11 output=7 actual_usd=0.125 actual_by=harness cost=actual"}})
	}
	for i := 0; i < machines; i++ {
		add(fmt.Sprintf("machine-%d", i), "m1", sprint.DoneOK)
	}
	add("control-usage", "control", sprint.Ready)
	for _, row := range rows {
		for _, col := range []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed} {
			add(row.Name+"-"+col, sprint.FriendRow(row.Name), col)
		}
	}
	for start := 0; start < len(members); start += ntable.LimitChangedEntries {
		end := min(start+ntable.LimitChangedEntries, len(members))
		_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(shapes[0].Revision), OperationID: fmt.Sprintf("usage-%d", start), Members: members[start:end]})
		require.NoError(t, err)
		shapes, err = h.m.Shapes(h.ctx, []string{"t-fleet"})
		require.NoError(t, err)
	}
	return rows
}

// The projection is exact across every lifecycle cell, while machine/control
// records are never fetched. A moved or missing record retries the whole read;
// a storage failure remains a failure (SPEC-SPRINT, friends.tokens).
func TestFriendUsageProjectionPreservesTotalsAndRetriesMovedRecords(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"none", "revision", "missing", "unplaced", "error", "cleared"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			rows := fillFriendUsage(t, h, 100)
			full, err := h.st.Load(h.ctx, []string{sprint.Fleet}, nil)
			require.NoError(t, err)
			obs := &friendFleetObservation{fault: fault}
			h.st.B = &friendFleetProbe{Mem: h.m, observe: obs}
			selected, err := h.st.friendFleet(h.ctx, rows)
			if fault == "cleared" {
				require.ErrorIs(t, err, errCleared)
				assert.Equal(t, 1, obs.shapeReads)
				assert.Zero(t, obs.reads)
				return
			}
			if fault == "error" {
				require.ErrorContains(t, err, "record read failed")
				return
			}
			require.NoError(t, err)
			for _, row := range rows {
				var all, got []*sprint.Card
				for _, col := range []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed} {
					all = append(all, full.Fleet.Cell(sprint.FriendRow(row.Name), col)...)
					got = append(got, selected.Cell(sprint.FriendRow(row.Name), col)...)
				}
				require.EqualValues(t, 72, sprint.FriendTokensFromCards(all))
				require.Equal(t, "0.5", sprint.FriendChargedFromCards(all))
				assert.Equal(t, sprint.FriendTokensFromCards(all), sprint.FriendTokensFromCards(got))
				assert.Equal(t, sprint.FriendChargedFromCards(all), sprint.FriendChargedFromCards(got))
				assert.Len(t, got, 4)
			}
			require.NotEmpty(t, obs.shapes)
			for _, shape := range obs.shapes {
				var names, cols []string
				for _, row := range shape.Rows {
					names = append(names, row.Key)
				}
				for _, col := range shape.Columns {
					cols = append(cols, col.Name)
				}
				assert.ElementsMatch(t, []string{"friend.alpha", "friend.beta"}, names)
				assert.ElementsMatch(t, []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed}, cols)
			}
			for _, id := range obs.ids {
				assert.NotContains(t, id, "machine-")
				assert.NotContains(t, id, "control-")
			}
			if fault != "none" {
				assert.Greater(t, obs.reads, 1, "revision/missing/placement retries rather than returning partial totals")
			}
		})
	}
}
