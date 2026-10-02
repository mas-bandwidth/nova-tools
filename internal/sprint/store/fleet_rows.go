package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The fleet rows of members with no machine row (docs/SPEC-SPRINT.md, section 5,
// the fleet from the inventory; sprint/fleet_sync.go). The sync's step takes such
// a member's control card off the fleet table, under the fence, once no card stays
// on it; a row is the table layer's, outside any batch, so its delete is a write of
// its own. It is decided and made under the fence (fenceLocked): the rows are read
// at a generation, the fence is taken at that generation, and only then are they
// deleted, so no step (a fleet up's release, a tick's deal) and no rejoin can come
// between the reading and the delete. A machine row that comes back places the same
// control card again before the sync's step reads the table, under the fence too: a
// batch never places a removed member, the table layer's cell add does.

// fenceLocked reads the fleet and work tables (and extras) at a generation, takes
// the fence at that generation as a lock (lock.go), runs fn on what it read, and
// releases the lock unwritten: fn's decisions stand on a read nothing has changed
// since. A generation that moved between the read and the take is read again.
func (st *Store) fenceLocked(ctx context.Context, verb string, extras func(*sprint.Snapshot) map[string][]string, fn func(pinned *Store, s *sprint.Snapshot) error) error {
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	r := pinned.retry(ctx)
	for r.next(pinned.attempts()) {
		snap, gen, err := pinned.Fenced(ctx, []string{sprint.Fleet, sprint.Work}, extras, nil)
		if err != nil {
			return err
		}
		lock := OpRecord{ID: "fleet-rows-" + pinned.newID() + "-lock", Verb: verb + " lock", At: pinned.now(), Lock: true}
		ok, err := pinned.B.Acquire(ctx, gen, lock)
		if err != nil {
			return err
		}
		if !ok {
			continue // another writer moved the generation: read again
		}
		ferr := fn(pinned, snap)
		return errors.Join(ferr, pinned.B.Release(ctx, lock, false))
	}
	return fmt.Errorf("%s: the sprint kept changing under it (%d tries); nothing was changed; run it again", verb, r.tries)
}

// DropMembers deletes, under the fence, the fleet row of every member whose
// control card is off the table (a sync's step took it off) and that keep does
// not name, with its beat record: a machine still beating is then a stranger the
// tick names, and teardown finds no key of a row it can no longer read. It is the
// members whose rows it deleted.
func (st *Store) DropMembers(ctx context.Context, keep []string) ([]string, error) {
	var gone []string
	err := st.fenceLocked(ctx, "fleet rows drop", nil, func(pinned *Store, s *sprint.Snapshot) error {
		gone = nil
		for _, m := range s.Fleet.Rows() {
			if s.MemberCtl(m) == nil && !slices.Contains(keep, m) {
				gone = append(gone, m)
			}
		}
		if len(gone) == 0 {
			return nil
		}
		// the keys first and the row last: a cleanup cut short leaves the row,
		// which the next sync reads as drift (DriftRemove) and finishes
		keys := make([]string, len(gone))
		for i, m := range gone {
			keys[i] = pinned.Names.Key(beatKey(m))
		}
		if _, err := pinned.B.DeleteKeys(ctx, keys); err != nil {
			return err
		}
		return pinned.B.RowsDel(ctx, pinned.Names.Table(sprint.Fleet), gone)
	})
	return gone, err
}

// RejoinMembers places again, under the fence, the control card of each named
// member that a sync took off the fleet table in this epoch (its record kept, on
// no cell), its row added first when the row is gone; the card comes back as the
// sync left it, held by the sync, so the sync (or fleet up) that follows releases
// it. The records are read in read sets of at most the table's bound each, however
// long the inventory. It is the members it placed; a member with a placed control
// card, or none at all, is left as it is.
func (st *Store) RejoinMembers(ctx context.Context, members []string) ([]string, error) {
	// a look with no lock first: a store with no removed member to place again
	// (every fleet up, nearly every sync) takes no fence; the decision to write is
	// made only under it, below
	if off, err := st.offTable(ctx, members); err != nil || len(off) == 0 {
		return nil, err
	}
	var out []string
	err := st.fenceLocked(ctx, "fleet rows rejoin", sprint.NamedExtras(sprint.Fleet, ctlIDs(members)), func(pinned *Store, s *sprint.Snapshot) error {
		out = nil
		table := pinned.Names.Table(sprint.Fleet)
		for _, m := range members {
			if c := s.Fleet.Card(sprint.CtlID(m)); c == nil || c.Placed() {
				continue
			}
			if !s.Fleet.HasRow(m) {
				if err := pinned.B.RowsAdd(ctx, table, []string{m}); err != nil {
					return err
				}
			}
			if err := pinned.B.Place(ctx, table, m, sprint.Ctl, pinned.sid(sprint.CtlID(m)), 0); err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}

// offTable is the members whose control card has a record on no cell of the
// fleet table, read in read sets of at most the table's bound each.
func (st *Store) offTable(ctx context.Context, members []string) ([]string, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	ids := pinned.sids(ctlIDs(members))
	var off []string
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		rs, err := pinned.readSet(ctx, pinned.Names.Table(sprint.Fleet), ids[start:end])
		if err != nil {
			return nil, err
		}
		for i, id := range ids[start:end] {
			if rec, ok := rs.Member(id); ok && !rec.Placed {
				off = append(off, members[start+i])
			}
		}
	}
	return off, nil
}

// ctlIDs is the control card ids of the members.
func ctlIDs(members []string) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = sprint.CtlID(m)
	}
	return out
}
