package control

import (
	"fmt"
	"strings"
	"time"
)

// validateDesired checks that desired is one of the permitted fleet states.
func validateDesired(desired string) error {
	switch desired {
	case DesiredRun, DesiredPause, DesiredDrain, DesiredStop:
		return nil
	default:
		return fmt.Errorf("%w: invalid desired state %q (must be RUN, PAUSE, DRAIN, or STOP)", ErrInvalidState, desired)
	}
}

// validateScope checks that scope is "fleet".
func validateScope(scope string) error {
	if scope != ScopeFleet {
		return fmt.Errorf("%w: invalid scope %q (must be %q)", ErrInvalidState, scope, ScopeFleet)
	}
	return nil
}

func validateExpiry(desired string, expires *time.Time) error {
	if desired == DesiredRun {
		if expires == nil {
			return fmt.Errorf("%w: RUN state must include expires timestamp", ErrInvalidState)
		}
		return nil
	}
	if expires != nil {
		return fmt.Errorf("%w: %s must not include expires", ErrInvalidState, desired)
	}
	return nil
}

// ValidateState validates an existing or unmarshaled State record.
func ValidateState(st State) error {
	if st.Generation <= 0 {
		return fmt.Errorf("%w: generation %d must be positive", ErrInvalidState, st.Generation)
	}
	if err := validateDesired(st.Desired); err != nil {
		return err
	}
	if err := validateScope(st.Scope); err != nil {
		return err
	}
	if strings.TrimSpace(st.By) == "" {
		return fmt.Errorf("%w: by field cannot be empty", ErrInvalidState)
	}
	if st.At.IsZero() {
		return fmt.Errorf("%w: at timestamp cannot be zero", ErrInvalidState)
	}
	return validateExpiry(st.Desired, st.Expires)
}

// ValidateUpdate validates a state transition according to CAS and duration bounds.
func ValidateUpdate(currentGen int64, expectedGen int64, next State, now time.Time, maxRUN time.Duration) error {
	if expectedGen != currentGen {
		return fmt.Errorf("%w: expected generation %d, current generation is %d", ErrGenerationMismatch, expectedGen, currentGen)
	}
	expectedNextGen := expectedGen + 1
	if next.Generation != expectedNextGen {
		return fmt.Errorf("%w: next generation must be %d (generation n+1), got %d", ErrInvalidState, expectedNextGen, next.Generation)
	}
	if err := validateDesired(next.Desired); err != nil {
		return err
	}
	if err := validateScope(next.Scope); err != nil {
		return err
	}
	if strings.TrimSpace(next.By) == "" {
		return fmt.Errorf("%w: by field cannot be empty", ErrInvalidState)
	}
	if err := validateExpiry(next.Desired, next.Expires); err != nil {
		return err
	}

	if next.Desired == DesiredRun {
		if !next.Expires.After(now) {
			return fmt.Errorf("%w: RUN expires (%s) must be in the future (after %s)", ErrInvalidState, next.Expires.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
		}
		maxAllowed := now.Add(maxRUN)
		if next.Expires.After(maxAllowed) {
			return fmt.Errorf("%w: RUN expires (%s) exceeds maxRUN window of %s (latest allowed: %s)", ErrInvalidState, next.Expires.UTC().Format(time.RFC3339), maxRUN, maxAllowed.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

// ValidateAck validates a bench acknowledgement record.
func ValidateAck(ack Ack) error {
	if ack.Generation <= 0 {
		return fmt.Errorf("%w: generation %d must be positive", ErrInvalidAck, ack.Generation)
	}
	if strings.TrimSpace(ack.Owner) == "" {
		return fmt.Errorf("%w: owner cannot be empty", ErrInvalidAck)
	}
	if strings.ContainsAny(ack.Owner, "/\\:*?\"<>|\x00") || strings.Contains(ack.Owner, "..") {
		return fmt.Errorf("%w: owner %q contains invalid path characters", ErrInvalidAck, ack.Owner)
	}
	if strings.TrimSpace(ack.Bench) == "" {
		return fmt.Errorf("%w: bench cannot be empty", ErrInvalidAck)
	}
	if err := validateDesired(ack.Desired); err != nil {
		return fmt.Errorf("%w: ack desired invalid: %v", ErrInvalidAck, err)
	}
	if ack.Observed.IsZero() {
		return fmt.Errorf("%w: observed timestamp cannot be zero", ErrInvalidAck)
	}
	return nil
}
