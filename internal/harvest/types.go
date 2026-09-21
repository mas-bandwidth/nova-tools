package harvest

import (
	"errors"
	"fmt"
	"time"
)

// ActionType identifies the side-effect being committed by an effect-owner.
// SPEC-PULSE required test 5 requires that an authoritative RESULT, harvest push,
// and accept each require a separate RUN action token.
type ActionType string

const (
	// ActionRESULT is the effect of publishing an authoritative RESULT for a card.
	ActionRESULT ActionType = "result"

	// ActionPush is the harvest side-effect of pushing the card branch to the remote repository.
	ActionPush ActionType = "push"

	// ActionAccept is the harvest side-effect of opening or updating the card PR (accept).
	ActionAccept ActionType = "accept"
)

// AttemptCredentials represents the ownership credentials presented by a worker
// or harvest runner for a specific card attempt.
type AttemptCredentials struct {
	Card       string `json:"card"`
	Attempt    string `json:"attempt"`
	FenceEpoch int    `json:"fence_epoch"`
}

// ActionToken represents a bounded, typed RUN action token issued by fleet control.
// In SPEC-PULSE: "Publication, merge and land need their own durable, bounded action
// token acquired after checking the current generation. The side-effect owner checks
// that token and generation where it commits the effect; a shell precheck is insufficient."
type ActionToken struct {
	TokenID    string     `json:"token_id"`
	Card       string     `json:"card"`
	Attempt    string     `json:"attempt"`
	Action     ActionType `json:"action"`
	Generation int        `json:"generation"`
	Scope      string     `json:"scope"`
	Expires    time.Time  `json:"expires"`
}

// Errors returned by effect-owner verification.
var (
	// ErrStaleFence indicates that the presented fence epoch does not match
	// the card's current fence epoch in the lifecycle authority.
	ErrStaleFence = errors.New("harvest: stale fence epoch")

	// ErrMismatchedAttempt indicates that the attempt does not match the active attempt
	// on the card projection.
	ErrMismatchedAttempt = errors.New("harvest: attempt does not match current card claimant")

	// ErrCardNotFound indicates that the card does not exist in the lifecycle authority.
	ErrCardNotFound = errors.New("harvest: card not found in lifecycle authority")

	// ErrNotRun indicates that fleet control is not in the desired RUN state (e.g. PAUSE, DRAIN, STOP).
	ErrNotRun = errors.New("harvest: fleet control state is not RUN")

	// ErrTokenExpired indicates that the presented RUN action token has expired.
	ErrTokenExpired = errors.New("harvest: RUN action token expired")

	// ErrGenerationMismatch indicates that the action token generation does not match the current control generation.
	ErrGenerationMismatch = errors.New("harvest: action token generation does not match current control generation")

	// ErrActionMismatch indicates that the action token was issued for a different action.
	ErrActionMismatch = errors.New("harvest: action token action type mismatch")

	// ErrAttemptMismatch indicates that the action token was issued for a different attempt.
	ErrAttemptMismatch = errors.New("harvest: action token attempt mismatch")

	// ErrCardMismatch indicates that the action token was issued for a different card.
	ErrCardMismatch = errors.New("harvest: action token card mismatch")

	// ErrScopeMismatch indicates that the action token scope is not fleet.
	ErrScopeMismatch = errors.New("harvest: action token scope is not fleet")

	// ErrMissingToken indicates that an action token was not supplied.
	ErrMissingToken = errors.New("harvest: missing RUN action token")
)

// CardProjection holds the replayed current projection of a card from the lifecycle ledger.
type CardProjection struct {
	Card       string `json:"card"`
	Attempt    string `json:"attempt"`
	FenceEpoch int    `json:"fence_epoch"`
	State      string `json:"state"`
}

// ControlState represents a point-in-time snapshot of fleet control.
type ControlState struct {
	Generation int       `json:"generation"`
	Desired    string    `json:"desired"`
	Expires    time.Time `json:"expires"`
	Scope      string    `json:"scope"`
}

func (c AttemptCredentials) String() string {
	return fmt.Sprintf("card=%s attempt=%s fence_epoch=%d", c.Card, c.Attempt, c.FenceEpoch)
}

func (t ActionToken) String() string {
	return fmt.Sprintf("token=%s card=%s attempt=%s action=%s gen=%d scope=%s expires=%s",
		t.TokenID, t.Card, t.Attempt, t.Action, t.Generation, t.Scope, t.Expires.Format(time.RFC3339))
}
