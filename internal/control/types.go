package control

import (
	"errors"
	"fmt"
	"time"
)

// Desired state constants for fleet control.
const (
	DesiredRun   = "RUN"
	DesiredPause = "PAUSE"
	DesiredDrain = "DRAIN"
	DesiredStop  = "STOP"
)

// ScopeFleet is the required scope for fleet control.
const ScopeFleet = "fleet"

// Common sentinel errors.
var (
	ErrStateNotFound      = errors.New("control state not found")
	ErrGenerationMismatch = errors.New("generation mismatch")
	ErrInvalidAuthority   = errors.New("invalid authority")
	ErrInvalidState       = errors.New("invalid control state")
	ErrInvalidAck         = errors.New("invalid control ack")
	ErrLockTimeout        = errors.New("coordinator lock timeout")
)

// State is the durable fleet control record stored in control/state.json.
// Format: generation=<n> desired=<RUN|PAUSE|DRAIN|STOP> scope=fleet by=<identity> at=<RFC3339> reason=<token> expires=<RFC3339>
type State struct {
	Generation int64      `json:"generation"`
	Desired    string     `json:"desired"`
	Scope      string     `json:"scope"`
	By         string     `json:"by"`
	At         time.Time  `json:"at"`
	Reason     string     `json:"reason,omitempty"`
	Expires    *time.Time `json:"expires,omitempty"`
}

// IsExpired reports whether the RUN state has expired relative to now.
// Non-RUN states (PAUSE, DRAIN, STOP) do not expire and return false.
func (s State) IsExpired(now time.Time) bool {
	if s.Desired != DesiredRun {
		return false
	}
	if s.Expires == nil {
		return true
	}
	return !s.Expires.After(now)
}

// IsValidAuthority verifies that s grants valid RUN authority at now.
// Returns nil if valid, or an ErrInvalidAuthority error if invalid or expired.
func (s State) IsValidAuthority(now time.Time) error {
	if s.Scope != ScopeFleet {
		return fmt.Errorf("%w: scope is %q, expected %q", ErrInvalidAuthority, s.Scope, ScopeFleet)
	}
	if s.Desired != DesiredRun {
		return fmt.Errorf("%w: desired state is %s, not %s", ErrInvalidAuthority, s.Desired, DesiredRun)
	}
	if s.Expires == nil {
		return fmt.Errorf("%w: RUN state missing expires timestamp", ErrInvalidAuthority)
	}
	if !s.Expires.After(now) {
		return fmt.Errorf("%w: RUN authority expired at %s (now is %s)", ErrInvalidAuthority, s.Expires.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	return nil
}

// Ack is the per-bench acknowledgement record stored in control/acks/<owner>.json.
// Each acknowledgement owner binds exactly one bench and never grants admission.
// Format: generation=<n> owner=<name> bench=<name|coordinator> desired=<state> observed=<RFC3339>
type Ack struct {
	Generation int64     `json:"generation"`
	Owner      string    `json:"owner"`
	Bench      string    `json:"bench"`
	Desired    string    `json:"desired"`
	Observed   time.Time `json:"observed"`
}

// Status aggregates acknowledgements against desired fleet control state.
type Status struct {
	Generation int64  `json:"generation"`
	Desired    string `json:"desired"`
	Acked      int    `json:"acked"`
	Pending    int    `json:"pending"`
	Unknown    int    `json:"unknown"`
	Owned      int    `json:"owned"`
	Complete   bool   `json:"complete"`
}
