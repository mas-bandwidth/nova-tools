package lifecycle

import (
	"fmt"
)

var (
	ErrConflict  = fmt.Errorf("conflict")
	ErrRefused   = fmt.Errorf("refused")
	ErrMalformed = fmt.Errorf("malformed")
	ErrDegraded  = fmt.Errorf("degraded")
)

func marshalEvent(ev Event) ([]byte, error) {
	return marshalJSON(ev)
}

func validateEvent(ev Event) error {
	if !validID(ev.Card) {
		return fmt.Errorf("%w: card", ErrMalformed)
	}
	if ev.Idempotency == "" {
		return fmt.Errorf("%w: idempotency", ErrMalformed)
	}
	if ev.At == "" {
		return fmt.Errorf("%w: at", ErrMalformed)
	}
	if ev.Rev < 1 {
		return fmt.Errorf("%w: rev", ErrMalformed)
	}
	switch ev.New {
	case Claimed:
		if ev.Attempt == nil || !validID(*ev.Attempt) {
			return fmt.Errorf("%w: CLAIMED requires attempt", ErrMalformed)
		}
	case Starting:
		if err := requireInvocation(ev); err != nil {
			return err
		}
	case Started:
		if err := requireInvocation(ev); err != nil {
			return err
		}
		if ev.Worker == nil || *ev.Worker == "" {
			return fmt.Errorf("%w: STARTED requires worker", ErrMalformed)
		}
	case Unknown:
		if ev.Attempt == nil || !validID(*ev.Attempt) {
			return fmt.Errorf("%w: UNKNOWN requires attempt", ErrMalformed)
		}
		if ev.Raised == nil || !*ev.Raised {
			return fmt.Errorf("%w: UNKNOWN requires raised=true", ErrMalformed)
		}
		if ev.Reason == nil || !validWhy(*ev.Reason) {
			return fmt.Errorf("%w: UNKNOWN requires reason", ErrMalformed)
		}
	case Ready, Returned, Harvested:
	default:
		return fmt.Errorf("%w: state %s", ErrMalformed, ev.New)
	}
	return nil
}

func requireInvocation(ev Event) error {
	if ev.Attempt == nil || !validID(*ev.Attempt) {
		return fmt.Errorf("%w: %s requires attempt", ErrMalformed, ev.New)
	}
	if ev.Bench == nil || !validID(*ev.Bench) {
		return fmt.Errorf("%w: %s requires bench", ErrMalformed, ev.New)
	}
	if ev.Route == nil || !validID(*ev.Route) {
		return fmt.Errorf("%w: %s requires route", ErrMalformed, ev.New)
	}
	if ev.Source == nil || *ev.Source == "" {
		return fmt.Errorf("%w: %s requires pinned source", ErrMalformed, ev.New)
	}
	if ev.Job == nil || !validID(*ev.Job) {
		return fmt.Errorf("%w: %s requires job", ErrMalformed, ev.New)
	}
	if ev.Lease == nil || !validID(*ev.Lease) {
		return fmt.Errorf("%w: %s requires lease", ErrMalformed, ev.New)
	}
	if ev.Generation == nil {
		return fmt.Errorf("%w: %s requires generation", ErrMalformed, ev.New)
	}
	return nil
}
