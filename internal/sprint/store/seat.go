package store

import (
	"context"
	"encoding/json"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The coordinator's seat (docs/SPEC-SPRINT.md, "Handing over the seat"): the
// coordinator key names the holder; keySeat records the last change of it,
// written by the seat step's commit with the coordinator key, its log line and
// its note (Redis commit, Mem Release), so the seat never moves unrecorded.
// keyOwner names the sprint's owner, whose name a take carries. Both are the
// sprint's, kept by a clear, and teardown removes them.
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
