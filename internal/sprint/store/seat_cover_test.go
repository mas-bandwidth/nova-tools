package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// The seat (docs/SPEC-SPRINT.md, "Handing over the seat"): Seat reads the
// seat's record, Owner and SetOwner the owner's name, SeatStep plans the
// move, seatRecord is the seat key's bytes. No unit test reached any of
// them (0.0% each in the unit tier's per-function table); these tests do,
// through the package's own seams: Mem as the store that keeps keys,
// kvless as the one that keeps none, a Snapshot built by hand.

// aFixedTime is the moment the seat moved: a test's fixed clock, never the
// wall clock's.
var aFixedTime = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

// Seat reads back what seatRecord wrote to the seat key, ok while the seat
// has moved; it refuses quietly when the backend keeps no keys.
func TestSeatCoverSeatReadsTheRecordedChange(t *testing.T) {
	t.Parallel()
	moved := &sprint.SeatChange{Holder: "someone", From: "coord", At: aFixedTime, By: "coord", Reason: "handover at the hour"}
	record, err := seatRecord(moved)
	require.NoError(t, err, "the record of a plain change")
	tests := []struct {
		name    string
		st      *Store
		seed    string
		want    sprint.SeatChange
		wantOK  bool
		wantErr string
	}{
		{"a moved seat reads back its record", &Store{B: NewMem()}, record, *moved, true, ""},
		{"an unmoved seat has no record", &Store{B: NewMem()}, "", sprint.SeatChange{}, false, ""},
		{"an unreadable record is an error", &Store{B: NewMem()}, "{", sprint.SeatChange{}, false, "the machine's seat record is unreadable"},
		{"a backend that keeps no keys has no seat", &Store{B: &kvless{}}, "", sprint.SeatChange{}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.seed != "" {
				_, ok := tt.st.B.(KV)
				require.True(t, ok, "the seed goes through the KV seam")
				require.NoError(t, tt.st.B.(KV).SetKey(ctx, keySeat, tt.seed))
			}
			s, ok, err := tt.st.Seat(ctx)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, s)
		})
	}
}

// Owner is the name SetOwner recorded; with no keys it is none, and
// SetOwner refuses with errNoKeys.
func TestSeatCoverOwnerRoundTripsSetOwner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		st        *Store
		set       string
		want      string
		wantIsErr error
	}{
		{"the recorded owner comes back", &Store{B: NewMem()}, "owner-a", "owner-a", nil},
		{"no owner was recorded", &Store{B: NewMem()}, "", "", nil},
		{"a backend that keeps no keys refuses the name and holds no owner", &Store{B: &kvless{}}, "owner-a", "", errNoKeys},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := tt.st.SetOwner(ctx, tt.set)
			if tt.wantIsErr != nil {
				assert.ErrorIs(t, err, tt.wantIsErr)
			} else {
				require.NoError(t, err)
			}
			v, err := tt.st.Owner(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.want, v)
		})
	}
}

// SeatStep is the coordinator's named step: its plan is MoveSeat's, a given
// seat plans the change and its note, no coordinator refuses it whole.
func TestSeatCoverSeatStepPlansTheMove(t *testing.T) {
	t.Parallel()
	r := sprint.SeatReq{To: "someone", Who: "coord", Reason: " handover at the hour "}
	step := SeatStep(r)
	assert.True(t, step.Named, "the seat step applies all or none")
	assert.Equal(t, "coordinator", step.Verb)
	assert.Equal(t, ArgsOf(r), step.Args, "the step replays only for the same request")
	assert.NotNil(t, step.Plan)
	tests := []struct {
		name       string
		snap       *sprint.Snapshot
		wantSeat   *sprint.SeatChange
		wantNotes  []sprint.Note
		wantRefuse []sprint.Refusal
	}{
		{"the holder gives the seat",
			&sprint.Snapshot{Coordinator: "coord", Now: aFixedTime},
			&sprint.SeatChange{Holder: "someone", Generation: 2, From: "coord", At: aFixedTime, By: "coord", Reason: "handover at the hour"},
			[]sprint.Note{{Kind: sprint.Happened, Type: sprint.NSeat, At: aFixedTime, Who: "coord", To: "someone",
				What: "coord -> someone: handover at the hour, by coord"}},
			nil},
		{"no coordinator refuses the move whole",
			&sprint.Snapshot{Coordinator: "", Now: aFixedTime},
			nil, nil,
			[]sprint.Refusal{{Key: "someone", Why: "the sprint has no coordinator; run: nova-sprint init --coordinator <name>"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := step.Plan(tt.snap)
			assert.Equal(t, tt.wantSeat, p.Seat)
			assert.Equal(t, tt.wantNotes, p.Notes)
			assert.Equal(t, tt.wantRefuse, p.Refused)
		})
	}
}

// seatRecord is the seat change as the seat key holds it: the change's JSON,
// a time RFC 3339 cannot name refuses it.
func TestSeatCoverSeatRecordIsTheKeysBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		c       *sprint.SeatChange
		want    string
		wantErr string
	}{
		{"a given seat is the change's json",
			&sprint.SeatChange{Holder: "someone", Generation: 2, From: "coord", At: aFixedTime, By: "coord", Reason: "handover at the hour"},
			`{"holder":"someone","generation":2,"from":"coord","at":"2026-10-04T09:00:00Z","by":"coord","reason":"handover at the hour"}`,
			""},
		{"a taken seat carries the take",
			&sprint.SeatChange{Holder: "someone", Generation: 2, From: "coord", At: aFixedTime, By: "someone", Taken: true, ApprovedBy: "owner", Reason: "out of credits"},
			`{"holder":"someone","generation":2,"from":"coord","at":"2026-10-04T09:00:00Z","by":"someone","taken":true,"approved_by":"owner","reason":"out of credits"}`,
			""},
		{"a year no record can hold is refused",
			&sprint.SeatChange{At: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
			"", "year outside of range [0,9999]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := seatRecord(tt.c)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, tt.want, got)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, got)
			var back sprint.SeatChange
			require.NoError(t, json.Unmarshal([]byte(got), &back))
			assert.Equal(t, *tt.c, back, "the key's bytes read back as the change")
		})
	}
}
