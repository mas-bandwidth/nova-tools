package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The where record (docs/SPEC-SPRINT.md section 1): what where shows that the
// work table's count cells do not carry, the held cards and the landings of
// the last hour, counted by the tick from the sprint it reads anyway and kept
// in one key, so where reads no card (the owner's requirement of 2026-09-30,
// one read a tick, and where under 1 s at 3,000 cards: at 2,843 it read every
// waiting and landed card's record, 5 to 7 s on the live store, 2026-10-02).
//
// Held is not a count a step can keep by adding one: a card is held when it
// waits, through a need or its place in line, on a sentinel not released or a
// card admitted held, so one release or one need frees or holds a chain. The
// tick counts it whole (sprint.HeldBack) from its twin, which holds every card,
// whenever the work table has moved since the record was counted.

// keyWhere is the where record's key, under the deployment's prefix.
const keyWhere = "where" // STRING, the where record (JSON)

// WhereRecord is the where record: the epoch and the work table's revision it
// was counted at, the held cards there (sprint.HeldBack), and the landings
// LandingRate may count from then on (sprint.RecentLandings), in Unix seconds,
// oldest first.
type WhereRecord struct {
	Epoch    uint64  `json:"epoch"`
	Rev      uint64  `json:"rev"`
	Held     int     `json:"held"`
	Landings []int64 `json:"landings,omitempty"`
}

// whereOf is the where record of a snapshot holding every card of the work
// table, at now, for the machine's STOPPED spans.
func whereOf(s *sprint.Snapshot, m Machine, now time.Time) WhereRecord {
	var landed []time.Time
	for _, c := range s.Work.Column(sprint.Landed) {
		if at, err := time.Parse(time.RFC3339, c.F("landed")); err == nil {
			landed = append(landed, at)
		}
	}
	r := WhereRecord{Epoch: s.Epoch, Rev: s.Work.Revision, Held: sprint.HeldBack(s)}
	for _, at := range sprint.RecentLandings(landed, m.Spans, m.FirstStart(s.Cleared), now) {
		r.Landings = append(r.Landings, at.Unix())
	}
	return r
}

// readWhere is the where record as stored; false when there is none or it
// does not read.
func readWhere(raw string, ok bool) (WhereRecord, bool) {
	var r WhereRecord
	if !ok || json.Unmarshal([]byte(raw), &r) != nil {
		return WhereRecord{}, false
	}
	return r, true
}

// keepWhere is the tick's count of the where record: nothing when the record
// is of this epoch at the work table's revision (an idle tick reads the
// shape and the record, two exchanges); else the sprint from the twin, brought
// up to date, and the record written. A twin held by another step of this
// process, or a clear under the read, leaves it to the next tick.
func (st *Store) keepWhere(ctx context.Context, m Machine) error {
	kv, err := st.kv()
	if err != nil {
		return nil // a store that keeps no records: where counts the cards
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return err
	}
	raw, ok, err := kv.GetKey(ctx, keyWhere)
	if err != nil {
		return err
	}
	if r, ok := readWhere(raw, ok); ok && r.Epoch == st.epoch && r.Rev == shapes[0].Revision {
		return nil
	}
	tw := st.twin()
	if !tw.mu.TryLock() {
		return nil
	}
	snap, _, err := st.twinRead(withBudget(ctx), tw, All, tickExtras, nil)
	tw.mu.Unlock()
	if errors.Is(err, errCleared) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := json.Marshal(whereOf(snap, m, st.now()))
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyWhere, string(b))
}

// WhereFacts is what where shows beside the tables' counts: the machine's
// records (Records: the store keeps them), the held cards and the landing
// stamps for the rate.
type WhereFacts struct {
	Records   bool
	Machine   Machine
	Heartbeat Heartbeat
	Held      int
	Landed    []time.Time
}

// WhereFacts reads the machine's records and the where record in one exchange.
// The record is taken when it is of the pinned epoch and either counted at
// workRev, the work table's revision the caller read, or kept by the RUNNING
// machine's loop: its last tick recent (MachineSilence) and not failed, and the
// record counted at or after the revision that tick saw (the heartbeat's), so
// the record is at most the tick in flight behind. A loop that ticks and does
// not keep the record (a binary from before it, a tick whose count was passed
// over) has its heartbeat move past the record, and the record is not taken.
// Otherwise (no record of this epoch: before the first tick, or after a clear;
// a STOPPED machine whose table a verb moved since its last tick; no loop
// keeping it) the held cards and the stamps are read from the cards (HeldBack,
// LandedAt), as before the record; a read of the stamps that fails leaves none,
// and the rate is the whole sprint's average, never a failed view.
func (st *Store) WhereFacts(ctx context.Context, workRev uint64) (WhereFacts, error) {
	var f WhereFacts
	if kv, err := st.kv(); err == nil {
		vals, oks, err := getKeys(ctx, kv, []string{keyMachine, keyHeartbeat, keyWhere})
		if err != nil {
			return f, err
		}
		f.Records = true
		for i, v := range []any{&f.Machine, &f.Heartbeat} {
			if oks[i] {
				if err := json.Unmarshal([]byte(vals[i]), v); err != nil {
					return f, err
				}
			}
		}
		if r, ok := readWhere(vals[2], oks[2]); ok && r.Epoch == st.epoch && (r.Rev == workRev || st.keptBy(r, f.Machine, f.Heartbeat)) {
			f.Held = r.Held
			for _, s := range r.Landings {
				f.Landed = append(f.Landed, time.Unix(s, 0).UTC())
			}
			return f, nil
		}
	}
	var err error
	if f.Held, err = st.HeldBack(ctx); err != nil {
		return f, err
	}
	if f.Landed, err = st.LandedAt(ctx); err != nil {
		f.Landed = nil // the whole sprint's average (sprint.LandingRate)
	}
	return f, nil
}

// keptBy says the RUNNING machine's loop keeps the record: it ticks (its last
// tick within MachineSilence, not failed) and the record is counted at or
// after the work table's revision its last tick saw.
func (st *Store) keptBy(r WhereRecord, m Machine, hb Heartbeat) bool {
	return !st.ByHand && m.Running() && hb.Error == "" && st.now().Sub(hb.Alive()) <= MachineSilence && r.Rev >= hb.Revisions[0]
}
