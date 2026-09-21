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

func validateAgainst(p *Projection, ev Event) error {
	from := Ready
	curRev := 0
	if p != nil {
		from = p.State
		curRev = p.Rev
	}
	if ev.Prior == nil || *ev.Prior != from {
		return fmt.Errorf("%w: prior %v does not match %s", ErrMalformed, ev.Prior, from)
	}
	if ev.Rev != curRev+1 {
		return fmt.Errorf("%w: revision gap card=%s rev=%d", ErrMalformed, ev.Card, ev.Rev)
	}
	if !legalTransition(from, ev.New) {
		return fmt.Errorf("%w: illegal transition %s -> %s", ErrMalformed, from, ev.New)
	}
	if p != nil {
		if err := bindOnce(p.Attempt, ev.Attempt, "attempt"); err != nil {
			return err
		}
		if err := bindOnce(p.Source, ev.Source, "source"); err != nil {
			return err
		}
		if err := bindOnce(p.Bench, ev.Bench, "bench"); err != nil {
			return err
		}
		if err := bindOnce(p.Route, ev.Route, "route"); err != nil {
			return err
		}
		if err := bindOnce(p.Job, ev.Job, "job"); err != nil {
			return err
		}
		if err := bindOnce(p.Lease, ev.Lease, "lease"); err != nil {
			return err
		}
		if p.Generation != nil {
			if ev.Generation == nil || *ev.Generation != *p.Generation {
				return fmt.Errorf("%w: immutable generation", ErrMalformed)
			}
		}
		if err := monotonicLimits(p.Limits, ev.Limits); err != nil {
			return err
		}
	} else if err := validLimits(ev.Limits); err != nil {
		return err
	}
	return nil
}

func legalTransition(from, to string) bool {
	if from == to {
		switch from {
		case Claimed, Starting, Started, Unknown, Returned, Harvested:
			return true
		default:
			return false
		}
	}
	switch from {
	case Ready:
		return to == Claimed
	case Claimed:
		return to == Starting
	case Starting:
		return to == Started || to == Unknown
	case Started:
		return to == Returned || to == Unknown
	case Unknown:
		return to == Started
	case Returned:
		return to == Harvested || to == Unknown
	}
	return false
}

func bindOnce(have, next *string, name string) error {
	if have == nil {
		return nil
	}
	if next == nil || *next != *have {
		return fmt.Errorf("%w: immutable %s", ErrMalformed, name)
	}
	return nil
}

func validLimits(lim *Limits) error {
	if lim == nil || lim.Attempts < 1 || lim.Max < 1 || lim.Max < lim.Attempts {
		return fmt.Errorf("%w: limits", ErrMalformed)
	}
	return nil
}

func monotonicLimits(have, next *Limits) error {
	if err := validLimits(next); err != nil {
		return err
	}
	if have == nil {
		return nil
	}
	if next.Attempts < have.Attempts || next.Max < have.Max {
		return fmt.Errorf("%w: limits went backwards", ErrMalformed)
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
