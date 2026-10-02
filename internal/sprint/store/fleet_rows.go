package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
// to the member after it, keep the row, whenever they ran. The beat records the
// deleted rows leave are owed in a record of their own (keyDropDebt) until deleted. A machine row that comes
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

// keyDropDebt is the members whose rows a fleet sync's cleanup deletes and whose
// beat records it still owes a delete: written before the rows are deleted and
// emptied once their beat records are, so a cleanup cut short after its row
// delete is finished by the next sync, which finds no row left to read
// (FinishDrops). Teardown removes it, and the beats it names.
const keyDropDebt = "fleet-drop-debt"

// dropDebtTokens returns the raw incarnation tokens stored in keyDropDebt.
func (st *Store) dropDebtTokens(ctx context.Context) ([]string, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil // a store that keeps no records keeps no beats to owe
	}
	raw, ok, err := kv.GetKey(ctx, keyDropDebt)
	if err != nil || !ok || raw == "" {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("the fleet cleanup's record %s cannot be read: %w", keyDropDebt, err)
	}
	return out, nil
}

// dropDebt is the members the cleanup owes a beat delete; none on a store that
// keeps no records. It extracts bare member names from stored incarnation tokens.
func (st *Store) dropDebt(ctx context.Context) ([]string, error) {
	tokens, err := st.dropDebtTokens(ctx)
	if err != nil || len(tokens) == 0 {
		return nil, err
	}
	var members []string
	for _, tok := range tokens {
		m, _ := parseDebtItem(tok)
		if m != "" {
			members = append(members, m)
		}
	}
	slices.Sort(members)
	return slices.Compact(members), nil
}

// formatDebtItem returns the incarnation token for a member drop obligation.
func formatDebtItem(member string, rev uint64) string {
	return fmt.Sprintf("%s:%d", member, rev)
}

// parseDebtItem extracts the member name and revision from a debt token.
// Unversioned tokens (legacy entries without a colon) parse with revision 0.
func parseDebtItem(item string) (member string, rev uint64) {
	m, revStr, ok := strings.Cut(item, ":")
	if !ok {
		return item, 0
	}
	r, _ := strconv.ParseUint(revStr, 10, 64)
	return m, r
}

func (st *Store) putDropDebt(ctx context.Context, debt []string) error {
	kv, err := st.rootKV()
	if err != nil {
		return nil // a store that keeps no records keeps no beats to owe
	}
	slices.Sort(debt)
	b, err := json.Marshal(slices.Compact(debt))
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyDropDebt, string(b))
}

func (st *Store) modifyDropDebt(ctx context.Context, fn func(current []string) []string) error {
	kv, err := st.rootKV()
	if err != nil {
		return nil // a store that keeps no records keeps no beats to owe
	}
	for tries := 0; tries < 5; tries++ {
		raw, ok, err := kv.GetKey(ctx, keyDropDebt)
		if err != nil {
			return err
		}
		var current []string
		if ok && raw != "" {
			if err := json.Unmarshal([]byte(raw), &current); err == nil {
				// current successfully read
			} else {
				return fmt.Errorf("the fleet cleanup's record %s cannot be read: %w", keyDropDebt, err)
			}
		}
		updated := fn(current)
		slices.Sort(updated)
		compacted := slices.Compact(updated)
		b, err := json.Marshal(compacted)
		if err != nil {
			return err
		}
		if err := kv.SetKey(ctx, keyDropDebt, string(b)); err != nil {
			continue
		}
		return nil
	}
	return fmt.Errorf("modifyDropDebt: failed to update %s after contention retries", keyDropDebt)
}

func (st *Store) addDropDebt(ctx context.Context, items []string) error {
	if len(items) == 0 {
		return nil
	}
	return st.modifyDropDebt(ctx, func(current []string) []string {
		return append(current, items...)
	})
}

func (st *Store) ackDropDebt(ctx context.Context, done []string) error {
	if len(done) == 0 {
		return nil
	}
	return st.modifyDropDebt(ctx, func(current []string) []string {
		var remaining []string
		for _, item := range current {
			if !slices.Contains(done, item) {
				remaining = append(remaining, item)
			}
		}
		return remaining
	})
}

// DropMembers deletes the fleet row of every member whose control card is off
// the table (a sync's step took it off) and that keep does not name, each only
// while its control card is still on no cell at the revision read here, checked
// and deleted as one atomic change (RowsDelIf): a member placed again in between,
// and anything dealt to it after, keep the row. The members are recorded as owed
// a beat delete before the rows go (keyDropDebt), and their beat records are then
// deleted (FinishDrops): a cleanup cut short at any point is finished by the next
// sync. A machine still beating after its row is gone is a stranger the tick
// names. It is the members whose rows it deleted.
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
	// the control cards' records, on no cell, read with the rows (in bounded read sets)
	extras := func(s *sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Fleet: ctlIDs(off(s))}
	}
	s, err := pinned.Load(ctx, []string{sprint.Fleet, sprint.Work}, extras)
	if err != nil {
		return nil, err
	}
	var guards []RowGuard
	var debtItems []string
	for _, m := range off(s) {
		rec := s.Fleet.Card(sprint.CtlID(m))
		if rec == nil {
			continue // a row with no control card record at all is no member the sync removed
		}
		id := pinned.sid(sprint.CtlID(m))
		guards = append(guards, RowGuard{Row: m, ID: id, Key: pinned.Names.RecordKey(sprint.Fleet, id), Rev: rec.Rev})
		debtItems = append(debtItems, formatDebtItem(m, rec.Rev))
	}
	var deleted []string
	if len(guards) > 0 {
		if err := pinned.addDropDebt(ctx, debtItems); err != nil {
			return nil, err
		}
		if deleted, err = pinned.B.RowsDelIf(ctx, pinned.Names.Table(sprint.Fleet), guards); err != nil {
			return deleted, err
		}
	}
	return deleted, pinned.FinishDrops(ctx)
}

// FinishDrops deletes the beat record of every member the cleanup owes one
// (keyDropDebt) whose fleet row is gone and whose control card is still on no
// cell, each checked and deleted as one atomic change (KeysDelIf): a member
// whose row came back or whose card was placed again keeps its beat, so a
// rejoin's fresh beat is never deleted. A member placed again leaves the record
// with no delete; a member whose row is still there (its delete still to come,
// which the next sync's drift finds) stays in it. Every fleet sync that writes
// runs it, and one with nothing to write too.
func (st *Store) FinishDrops(ctx context.Context) error {
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	debt, err := pinned.dropDebtTokens(ctx)
	if err != nil || len(debt) == 0 {
		return err
	}
	slices.Sort(debt)
	debt = slices.Compact(debt)
	var members []string
	for _, item := range debt {
		m, _ := parseDebtItem(item)
		if m != "" {
			members = append(members, m)
		}
	}
	slices.Sort(members)
	members = slices.Compact(members)
	s, err := pinned.Load(ctx, []string{sprint.Fleet, sprint.Work}, sprint.NamedExtras(sprint.Fleet, ctlIDs(members)))
	if err != nil {
		return err
	}
	var guards []RowGuard
	var guardItems []string
	var completed []string
	for _, item := range debt {
		m, rev := parseDebtItem(item)
		rec := s.Fleet.Card(sprint.CtlID(m))
		switch {
		case rec == nil:
			// no record: debt is satisfied
			completed = append(completed, item)
		case rec.Placed():
			// placed again: if the active card revision is greater than the drop revision (or legacy rev 0),
			// this drop obligation was superseded by the rejoin
			if rec.Rev > rev || rev == 0 {
				completed = append(completed, item)
			}
		case s.Fleet.HasRow(m):
			// its row delete is still to come; keep in debt
		default:
			// row is gone, control card is unplaced
			if rec.Rev == rev || rev == 0 {
				id := pinned.sid(sprint.CtlID(m))
				guards = append(guards, RowGuard{
					Row: m, ID: id, Key: pinned.Names.RecordKey(sprint.Fleet, id), Rev: rec.Rev,
					Keys: []string{pinned.Names.Key(beatKey(m))},
				})
				guardItems = append(guardItems, item)
			} else if rec.Rev > rev {
				// card was removed at a newer revision; old drop is superseded
				completed = append(completed, item)
			}
		}
	}
	done, err := pinned.B.KeysDelIf(ctx, pinned.Names.Table(sprint.Fleet), guards)
	if err != nil {
		if len(completed) > 0 {
			_ = pinned.ackDropDebt(ctx, completed)
		}
		return err
	}
	for i, g := range guards {
		if slices.Contains(done, g.Row) {
			completed = append(completed, guardItems[i])
		}
	}
	return pinned.ackDropDebt(ctx, completed)
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
