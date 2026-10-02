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
// its own, and it is conditional at its commit: the row goes only while its control
// card is still on no cell at the revision the delete read (RowsDelIf, one atomic
// change), so a fleet up that placed the card again in between, and anything dealt
// to the member after it, keep the row, whenever they ran. A machine row that comes
// back places the same control card again before the sync's step reads the table,
// under the fence (fenceLocked): a batch never places a removed member, the table
// layer's cell add does.

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

// DropMembers deletes the fleet row of every member whose control card is off
// the table (a sync's step took it off) and that keep does not name, each only
// while its control card is still on no cell at the revision read here
// (RowsDelIf). The beat records go first and the rows last: a cleanup cut short
// leaves its rows, which the next sync reads as drift and finishes; a member
// placed again in between keeps its row, and its next beat writes its record
// again. A machine still beating after its row is gone is a stranger the tick
// names, and teardown finds no key of a row it can no longer read. It is the
// members whose rows it deleted.
func (st *Store) DropMembers(ctx context.Context, keep []string) ([]string, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	off := func(s *sprint.Snapshot) []string {
		var out []string
		for _, m := range s.Fleet.Rows() {
			if s.MemberCtl(m) == nil && !slices.Contains(keep, m) {
				out = append(out, m)
			}
		}
		return out
	}
	// the control cards' records, on no cell, read with the rows
	extras := func(s *sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Fleet: ctlIDs(off(s))}
	}
	s, err := pinned.Load(ctx, []string{sprint.Fleet, sprint.Work}, extras)
	if err != nil {
		return nil, err
	}
	var guards []RowGuard
	var keys []string
	for _, m := range off(s) {
		rec := s.Fleet.Card(sprint.CtlID(m))
		if rec == nil {
			continue // a row with no control card record at all is no member the sync removed
		}
		guards = append(guards, RowGuard{Row: m, ID: pinned.sid(sprint.CtlID(m)), Rev: rec.Rev})
		keys = append(keys, pinned.Names.Key(beatKey(m)))
	}
	if len(guards) == 0 {
		return nil, nil
	}
	if _, err := pinned.B.DeleteKeys(ctx, keys); err != nil {
		return nil, err
	}
	return pinned.B.RowsDelIf(ctx, pinned.Names.Table(sprint.Fleet), guards)
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
