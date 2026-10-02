package store

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The fleet rows of members with no machine row (docs/SPEC-SPRINT.md, section 5,
// the fleet from the inventory; sprint/fleet_sync.go). The sync's step takes such
// a member's control card off the fleet table, under the fence, once no card stays
// on it; a row is the table layer's, outside any batch, so the verb deletes it
// after the step: a row whose control card is off the table is dealt nothing, so
// nothing can land on it between the two. A machine row that comes back places
// the same control card again before the sync's step reads the table: a batch
// never places a removed member, the table layer's cell add does.

// DropMembers deletes the fleet rows of members whose control cards a sync took
// off the table, and their beat records: a machine still beating is then a
// stranger the tick names, and teardown finds no key of a row it can no longer
// read.
func (st *Store) DropMembers(ctx context.Context, members []string) error {
	if len(members) == 0 {
		return nil
	}
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	if err := pinned.B.RowsDel(ctx, pinned.Names.Table(sprint.Fleet), members); err != nil {
		return err
	}
	keys := make([]string, len(members))
	for i, m := range members {
		keys[i] = pinned.Names.Key(beatKey(m))
	}
	_, err = pinned.B.DeleteKeys(ctx, keys)
	return err
}

// RejoinMembers places again the control card of each named member that a sync
// took off the fleet table in this epoch (its record kept, on no cell), its row
// added first when the row is gone; the card comes back as the sync left it,
// held by the sync, so the sync (or fleet up) that follows releases it. It is the
// members it placed; a member with a placed control card, or none at all, is left
// as it is.
func (st *Store) RejoinMembers(ctx context.Context, members []string) ([]string, error) {
	if len(members) == 0 {
		return nil, nil
	}
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	table := pinned.Names.Table(sprint.Fleet)
	shapes, err := pinned.B.Shapes(ctx, []string{table})
	if err != nil || len(shapes) == 0 {
		return nil, err
	}
	rows := map[string]bool{}
	for _, r := range shapes[0].Rows {
		rows[r.Key] = true
	}
	rs, err := pinned.readSet(ctx, table, pinned.sids(ctlIDs(members)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range members {
		rec, ok := rs.Member(pinned.sid(sprint.CtlID(m)))
		if !ok || rec.Placed {
			continue
		}
		if !rows[m] {
			if err := pinned.B.RowsAdd(ctx, table, []string{m}); err != nil {
				return out, err
			}
		}
		if err := pinned.B.Place(ctx, table, m, sprint.Ctl, pinned.sid(sprint.CtlID(m)), 0); err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// ctlIDs is the control card ids of the members.
func ctlIDs(members []string) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = sprint.CtlID(m)
	}
	return out
}
