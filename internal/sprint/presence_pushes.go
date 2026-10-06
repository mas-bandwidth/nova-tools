package sprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The seat's other pushes live on the same JSON document as PushRecord
// (seat-push:<name>), so teardown's deletion of that key takes them too.
// A proof older than its period times PushSetLives is stale.
const (
	BusPushEvery      = time.Minute      // nova-bus recv --forever, as the coordinator
	FriendsWatchEvery = 10 * time.Minute // nova-sprint friends watch
	StatusWatchEvery  = time.Minute      // nova-sprint status watch
	PushSetLives      = 3
)

// The JSON fields of the three watch proofs.
const (
	PushFieldBus         = "bus"
	PushFieldFriends     = "friends"
	PushFieldTransitions = "transitions"
)

// SeatPushes is one name's push record and the three watch proofs beside it.
type SeatPushes struct {
	PushRecord
	BusAt         time.Time `json:"bus_at,omitzero"`
	FriendsAt     time.Time `json:"friends_at,omitzero"`
	TransitionsAt time.Time `json:"transitions_at,omitzero"`
}

// DecodeSeatPushes reads a seat-push document. A bad document is one fixed
// sentence: the bytes are never part of the error.
func DecodeSeatPushes(raw string) (SeatPushes, error) {
	var s SeatPushes
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, errors.New("the seat push record is not JSON")
	}
	return s, nil
}

// Beat stamps field at now. field is PushFieldBus, PushFieldFriends or
// PushFieldTransitions.
func (s *SeatPushes) Beat(field string, now time.Time) error {
	switch field {
	case PushFieldBus:
		s.BusAt = now
	case PushFieldFriends:
		s.FriendsAt = now
	case PushFieldTransitions:
		s.TransitionsAt = now
	default:
		return fmt.Errorf("no push field %s", field)
	}
	return nil
}

// At is the time field was last stamped, and the period that proof stands for.
func (s SeatPushes) At(field string) (time.Time, time.Duration, bool) {
	switch field {
	case PushFieldBus:
		return s.BusAt, BusPushEvery, true
	case PushFieldFriends:
		return s.FriendsAt, FriendsWatchEvery, true
	case PushFieldTransitions:
		return s.TransitionsAt, StatusWatchEvery, true
	}
	return time.Time{}, 0, false
}

// WatchStale says at is missing or older than every times PushSetLives.
// Exactly that age still stands: stale is older than the bound.
func WatchStale(at time.Time, every time.Duration, now time.Time) bool {
	if at.IsZero() {
		return true
	}
	return now.Sub(at) > every*PushSetLives
}
