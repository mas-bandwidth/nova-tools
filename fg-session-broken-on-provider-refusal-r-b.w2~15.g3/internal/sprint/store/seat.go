package store

import (
	"context"
	"encoding/json"
	"time"

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

// SeatCheck is the seat state with what the key must agree with
// (seat-key-follows-record.w2): Record is the holder the seat's record names
// ("" while the seat has not moved since init, init's key being its record);
// Server is the actor the run loop runs as ("" when no server's record is
// fresh); Drift is how the key (Holder), the record and the server disagree
// (sprint.SeatDrift), "" when they do not.
type SeatCheck struct {
	SeatState
	Record string `json:"record"`
	Server string `json:"server"`
	Drift  string `json:"drift,omitempty"`
}

// SeatCheck reads the seat state, the seat's record and the server's record.
func (st *Store) SeatCheck(ctx context.Context) (SeatCheck, error) {
	var s SeatCheck
	var err error
	if s.SeatState, err = st.SeatState(ctx); err != nil {
		return s, err
	}
	rec, ok, err := st.Seat(ctx)
	if err != nil {
		return s, err
	}
	if ok {
		s.Record = rec.Holder
	}
	if s.Server, err = st.ServerActor(ctx); err != nil {
		return s, err
	}
	s.Drift = sprint.SeatDrift(s.Holder, s.Record, s.Server)
	return s, nil
}

// InitSeat is init's write of the coordinator key: the seat record's holder
// when the seat has a record, else first, the name init was given (--coordinator,
// else its actor). It returns the name the key holds. The run loop never
// writes the key; init and the seat's steps alone do, from the record.
func (st *Store) InitSeat(ctx context.Context, first string) (string, error) {
	rec, ok, err := st.Seat(ctx)
	if err != nil {
		return "", err
	}
	name := first
	if ok {
		name = rec.Holder
	}
	return name, st.B.SetCoordinator(ctx, name)
}

// SeatRepairStep writes the coordinator key from the seat's record, read now
// (sprint.RepairSeat): the record's holder or the owner repairs it, with a
// reason, logged with who and why. A seat with no record, or whose key says
// what its record does, is refused with nothing written.
func (st *Store) SeatRepairStep(ctx context.Context, r sprint.SeatRepairReq) (Step, string, error) {
	key, err := st.B.Coordinator(ctx)
	if err != nil {
		return Step{}, "", err
	}
	rec, ok, err := st.Seat(ctx)
	if err != nil {
		return Step{}, "", err
	}
	if why := sprint.NotSeatRepair(key, rec, ok, r); why != "" {
		return Step{}, why, nil
	}
	return Step{Named: true, Args: ArgsOf(r), Verb: "seat",
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RepairSeat(s, rec, r) }}, "", nil
}

// keyServer is the server's record: the actor the run loop runs as and when it
// last said so. The run loop writes it at its start and every ServerEvery, for
// ServerTTL on a store that expires keys, so a torn-down sprint keeps none past
// it; it is read as the server's while it is no older than ServerTTL.
const keyServer = "server"

// ServerEvery is how often the run loop writes its record; ServerTTL is how
// long a record stands for a server.
const (
	ServerEvery = 30 * time.Second
	ServerTTL   = 2 * time.Minute
)

// serverRecord is the server's record as keyServer holds it.
type serverRecord struct {
	Actor string    `json:"actor"`
	At    time.Time `json:"at"`
}

// expiringKV is a backend whose keys can expire (Redis): the server's record is
// written for ServerTTL. A KV without it keeps the record until it is written
// again, and the reader judges it by its time.
type expiringKV interface {
	SetKeyFor(ctx context.Context, name, value string, ttl time.Duration) error
}

// SetServerActor records the actor the run loop runs as, at the clock's
// reading: the server's report of itself, which seat and handover show. It
// writes nothing else, and never the coordinator key.
func (st *Store) SetServerActor(ctx context.Context, actor string) error {
	kv, ok := st.B.(KV)
	if !ok {
		return nil // a backend that keeps no keys has no server's record
	}
	b, err := json.Marshal(serverRecord{Actor: actor, At: st.now()})
	if err != nil {
		return err
	}
	if x, ok := st.B.(expiringKV); ok {
		return x.SetKeyFor(ctx, keyServer, string(b), ServerTTL)
	}
	return kv.SetKey(ctx, keyServer, string(b))
}

// ServerActor is the actor the server runs as, by its record: "" with no
// record, or one older than ServerTTL.
func (st *Store) ServerActor(ctx context.Context) (string, error) {
	kv, ok := st.B.(KV)
	if !ok {
		return "", nil
	}
	raw, ok, err := kv.GetKey(ctx, keyServer)
	if err != nil || !ok {
		return "", err
	}
	var r serverRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return "", err
	}
	if st.now().Sub(r.At) > ServerTTL {
		return "", nil
	}
	return r.Actor, nil
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
