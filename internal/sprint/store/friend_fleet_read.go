package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// friendFleet reads the four lifecycle cells used by FriendTokensFromCards and
// FriendChargedFromCards (SPEC-SPRINT, friends.tokens), at one table revision.
// Machine cards and other friend cells contribute neither total and are not read.
func (st *Store) friendFleet(ctx context.Context, rows []FriendRow) (*sprint.Table, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(rows))
	for _, row := range rows {
		wanted[sprint.FriendRow(row.Name)] = true
	}
	r := st.retry(ctx)
	for r.next(LoadTries) {
		fleet, err := st.friendFleetOnce(ctx, wanted)
		if !errors.Is(err, errMoved) {
			return fleet, err
		}
	}
	return nil, fmt.Errorf("the tables are busy: the fleet table kept changing while friend usage was read, %d reads in %s", r.tries, r.slept().Round(time.Millisecond))
}

// friendFleetOnce preserves column positions while projecting the token cells;
// readFriendFleetInto holds Load's missing/placement/revision checks and retry.
func (st *Store) friendFleetOnce(ctx context.Context, wanted map[string]bool) (*sprint.Table, error) {
	shapes, err := st.shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
	if err != nil {
		return nil, err
	}
	shape := shapes[0]
	if st.pinned && shape.Epoch != st.epoch {
		return nil, errCleared
	}
	shape.Rows = slices.DeleteFunc(slices.Clone(shape.Rows), func(row ntable.Row) bool { return !wanted[row.Key] })
	shape = cellsOf(shape, []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed})
	fleet := sprint.NewTable(sprint.Fleet)
	fleet.Epoch, fleet.Revision = shape.Epoch, shape.Revision
	if len(shape.Rows) == 0 {
		return fleet, nil
	}
	ids, err := st.B.CellIDs(ctx, []ntable.Table{shape})
	if err != nil {
		return nil, err
	}
	if err = st.readFriendFleetInto(ctx, fleet, ids[shape.Name]); err != nil {
		return nil, err
	}
	return fleet, nil
}

// readFriendFleetInto batches up to eight existing read sets per exchange. This
// covers the current usage projection in one flight. Queued commands stay bounded
// as history grows; exact totals still retain all selected cards for their fold.
// Each chunk keeps Load's revision/missing/placement checks (SPEC-NOVA-TABLE).
func (st *Store) readFriendFleetInto(ctx context.Context, fleet *sprint.Table, ids []string) error {
	r := redisBackend(st.B)
	if r == nil {
		return st.readInto(ctx, fleet, ids, true)
	}
	const groupMembers = 8 * ntable.LimitReadSetMembers
	for base := 0; base < len(ids); base += groupMembers {
		pipe := r.C.Pipeline()
		var queued []*ntable.ReadSetCmd
		limit := min(base+groupMembers, len(ids))
		for start := base; start < limit; start += ntable.LimitReadSetMembers {
			var cmd *ntable.ReadSetCmd
			var err error
			chunk := ids[start:min(start+ntable.LimitReadSetMembers, limit)]
			if r.Old {
				cmd, err = ntable.QueueReadSet(ctx, pipe, st.Names.Table(sprint.Fleet), ntable.ReadSetScope{Members: chunk, Fields: []string{sprint.FieldUsage}}, r.Pinned)
			} else {
				cmd, err = ntable.QueueReadSet(ctx, pipe, st.Names.Table(sprint.Fleet), ntable.ReadSetScope{Members: chunk, Fields: []string{sprint.FieldUsage}})
			}
			if err != nil {
				return err
			}
			queued = append(queued, cmd)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		for _, cmd := range queued {
			res, err := cmd.Result()
			if err != nil {
				return err
			}
			st.stats().rows.Add(int64(len(res.Members)))
			if res.Epoch != fleet.Epoch || res.Revision != fleet.Revision || len(res.Missing) > 0 {
				return &movedError{table: st.Names.Table(sprint.Fleet)}
			}
			for _, member := range res.Members {
				if !member.Placed {
					return &movedError{table: st.Names.Table(sprint.Fleet)}
				}
				fleet.Put(&sprint.Card{ID: sprint.CardID(member.ID), Score: member.Score, Rev: member.Revision, Row: member.Row, Col: member.Col, Fields: member.Fields})
			}
		}
	}
	return nil
}
