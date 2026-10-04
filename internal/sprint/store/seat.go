package store

import (
	"context"
	"encoding/json"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The coordinator's seat (docs/SPEC-SPRINT.md, "Handing over the seat"): the
// coordinator key names the holder; keySeat records the last change of it,
// written by the seat step's commit with the coordinator key, its log line and
// its note (Redis commit, Mem Release), so the seat never moves unrecorded,
// and carries the seat's generation (sprint.FirstSeatGeneration while there
// is no record). keyOwner names the sprint's owner, whose name a take
// carries. All are the sprint's, kept by a clear, and teardown removes them.
const (
	keySeat  = "seat"
	keyOwner = "owner"
)

// Seat is the last change of the seat, ok false when the seat has not moved
// since init.
func (st *Store) Seat(ctx context.Context) (sprint.SeatChange, bool, error) {
	var s sprint.SeatChange
	if _, ok := st.B.(KV); !ok {
		return s, false, nil // a backend that keeps no keys has no seat record
	}
	if err := st.getJSON(ctx, keySeat, &s); err != nil {
		return s, false, err
	}
	return s, s.Holder != "", nil
}

// SeatState is the seat as the friends' daemons read it every second
// (nova-sprint seat): the holder, the sprint's epoch and the seat's
// generation, from three keys and no table.
type SeatState struct {
	Holder     string `json:"holder"`
	Epoch      uint64 `json:"epoch"`
	Generation uint64 `json:"generation"`
}

// SeatState reads the seat: the coordinator key, the seat record and the epoch.
func (st *Store) SeatState(ctx context.Context) (SeatState, error) {
	var s SeatState
	var err error
	if s.Holder, err = st.B.Coordinator(ctx); err != nil {
		return s, err
	}
	if s.Generation, err = st.seatGeneration(ctx); err != nil {
		return s, err
	}
	es, err := st.EpochNow(ctx)
	s.Epoch = es.N
	return s, err
}

// seatGeneration is the seat's generation as the seat record holds it:
// FirstSeatGeneration with no record (the seat has not moved since init, or
// the backend keeps no keys).
func (st *Store) seatGeneration(ctx context.Context) (uint64, error) {
	kv, ok := st.B.(KV)
	if !ok {
		return sprint.FirstSeatGeneration, nil
	}
	raw, ok, err := kv.GetKey(ctx, keySeat)
	if err != nil {
		return 0, err
	}
	return seatGenerationOf(raw, ok)
}

// seatGenerationOf is the generation the seat record's bytes carry, raw read
// with ok as the key read: FirstSeatGeneration for no record, and for a record
// written before the seat carried one.
func seatGenerationOf(raw string, ok bool) (uint64, error) {
	if !ok {
		return sprint.FirstSeatGeneration, nil
	}
	var s sprint.SeatChange
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return 0, err
	}
	return max(s.Generation, sprint.FirstSeatGeneration), nil
}

// Owner is the sprint's owner as init recorded it, "" when none was.
func (st *Store) Owner(ctx context.Context) (string, error) {
	kv, ok := st.B.(KV)
	if !ok {
		return "", nil
	}
	v, _, err := kv.GetKey(ctx, keyOwner)
	return v, err
}

// SetOwner records the sprint's owner. It is refused when the backend keeps no
// keys.
func (st *Store) SetOwner(ctx context.Context, name string) error {
	kv, ok := st.B.(KV)
	if !ok {
		return errNoKeys
	}
	return kv.SetKey(ctx, keyOwner, name)
}

// SeatStep moves the seat (sprint.MoveSeat): its commit writes the coordinator
// key and the seat's record with the step's note and log line.
func SeatStep(r sprint.SeatReq) Step {
	return Step{Named: true, Args: ArgsOf(r), Verb: "coordinator",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.MoveSeat(s, r) }}
}

// seatRecord is the seat change as the seat key holds it.
func seatRecord(c *sprint.SeatChange) (string, error) {
	b, err := json.Marshal(c)
	return string(b), err
}
