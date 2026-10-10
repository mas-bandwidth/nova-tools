package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The stops record (internal/sprint/stops.go; the owner, 2026-10-10: no silent stops and no
// silent waits): every automatic stop that holds, with when it began, and what waits on the
// seat (the open judgments, those past their deadline, the oldest, the escalation level),
// counted by the tick from the snapshot it reads anyway and kept in one key, so where (the
// dashboard) reads it in the exchange it makes already, and `nova-sprint doctor` lists it.

// keyStops is the stops record's key, under the deployment's prefix.
const keyStops = "stops" // STRING, the stops record (JSON)

// StopsRefresh is how long the tick keeps a record that has not changed before it writes
// it again, so its At says the machine still counts it.
const StopsRefresh = time.Minute

// StopsRecord is the stops record: the epoch and the time it was counted at, what waits on
// the seat, and every automatic stop that holds.
type StopsRecord struct {
	Epoch uint64           `json:"epoch"`
	At    time.Time        `json:"at"`
	Seat  sprint.SeatWaits `json:"seat"`
	Stops []sprint.Stop    `json:"stops"`
}

// stopsOf is the record of a snapshot at now, for the tick's request (its beats, friends
// and STOPPED spans).
func stopsOf(s *sprint.Snapshot, r sprint.TickReq, now time.Time) StopsRecord {
	stops := sprint.LiveStops(s, r)
	if stops == nil {
		stops = []sprint.Stop{}
	}
	return StopsRecord{Epoch: s.Epoch, At: now.UTC(), Seat: sprint.SeatWaitsOf(s, r), Stops: stops}
}

// keepStops writes the record when it differs from the last this process wrote, or when
// that one is StopsRefresh old: an idle tick writes nothing. A store that keeps no records
// keeps none.
func (st *Store) keepStops(ctx context.Context, tw *Twin, rec StopsRecord) error {
	kv, err := st.kv()
	if err != nil {
		return nil
	}
	body := rec
	body.At = time.Time{}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	tw.stopsMu.Lock()
	same := tw.stopsKept == string(b) && rec.At.Sub(tw.stopsAt) < StopsRefresh
	tw.stopsMu.Unlock()
	if same {
		return nil
	}
	full, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := kv.SetKey(ctx, keyStops, string(full)); err != nil {
		return err
	}
	tw.stopsMu.Lock()
	tw.stopsKept, tw.stopsAt = string(b), rec.At
	tw.stopsMu.Unlock()
	return nil
}

// readStops is the stops record as stored; false when there is none or it does not read.
func readStops(raw string, ok bool) (StopsRecord, bool) {
	var r StopsRecord
	if !ok || json.Unmarshal([]byte(raw), &r) != nil {
		return StopsRecord{}, false
	}
	return r, true
}

// StopsKept is the stops record the tick last wrote, when it is of the pinned epoch; false
// when there is none (before the first tick, after a clear, or a store that keeps no records).
func (st *Store) StopsKept(ctx context.Context) (StopsRecord, bool, error) {
	kv, err := st.kv()
	if err != nil {
		return StopsRecord{}, false, nil
	}
	v, ok, err := kv.GetKey(ctx, keyStops)
	if err != nil {
		return StopsRecord{}, false, err
	}
	r, ok := readStops(v, ok)
	return r, ok && r.Epoch == st.epoch, nil
}

// StopsNow is the stops record counted now from a fresh read of the sprint, as the tick
// counts it (the members' beats, the friends' seats, the machine's STOPPED spans): what
// `nova-sprint doctor` lists, whether or not the machine ticks.
func (st *Store) StopsNow(ctx context.Context) (StopsRecord, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return StopsRecord{}, err
	}
	_, shapes, err := pinned.look(ctx)
	if err != nil {
		return StopsRecord{}, err
	}
	_, beats, err := pinned.fleetBeats(ctx, shapes)
	if err != nil {
		return StopsRecord{}, err
	}
	snap, _, err := pinned.Fenced(withBudget(ctx), All, tickExtras, nil)
	if err != nil {
		return StopsRecord{}, err
	}
	now := pinned.now()
	m, _, err := pinned.Machine(ctx)
	if err != nil {
		return StopsRecord{}, err
	}
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, Beats: beats}
	if req.Friends, err = pinned.friendSeats(ctx, snap, now); err != nil {
		return StopsRecord{}, err
	}
	return stopsOf(snap, req, now), nil
}

// Tell writes one happened note addressed to the coordinator, in a notes-only step of the
// machine's, so the push delivers it (inbox --push seat): what a verb that runs outside the
// tick (promote) found and the seat must hear of. typ is the note's type, what its text and
// hint the verb to run.
func (st *Store) Tell(ctx context.Context, verb, typ, what, hint string) error {
	return st.tellTick(ctx, verb, typ, what, hint)
}
