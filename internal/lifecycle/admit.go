package lifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"
)

// Coordinator is the small interface the admit path needs of fleet control.
// The control record itself is not this package's: WithCoordinator is the
// linearization point for RUN vs PAUSE. ConsumeStartToken is the admission
// check; a Desired read outside that lock is check-then-unlock.
type Coordinator interface {
	WithCoordinator(ctx context.Context, now time.Time, fn func(Admission) error) error
}

// Admission is the coordinator-held view inside WithCoordinator.
type Admission interface {
	ConsumeStartToken(attempt string) (StartToken, error)
}

// StartToken is bounded RUN authority consumed in CLAIMED->STARTING.
type StartToken struct {
	Generation int
	Scope      string
	Expires    time.Time
}

// AdmitStart consumes a bounded start token and records CLAIMED->STARTING
// inside WithCoordinator. The generation on the token is the admission
// linearization point. A token merely issued but not consumed is not admission.
func (s *Store) AdmitStart(ctx context.Context, now time.Time, ctrl Coordinator, attempt string) error {
	if s == nil {
		return fmt.Errorf("lifecycle: store is nil")
	}
	if ctrl == nil {
		return fmt.Errorf("lifecycle: coordinator is required; refusing to guess")
	}
	if !validID(attempt) {
		return fmt.Errorf("%w: attempt", ErrRefused)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.mutate(func() error {
		p, ok := s.byAttempt(attempt)
		if !ok {
			return fmt.Errorf("%w: attempt %s is not in the ledger", ErrRefused, attempt)
		}
		if p.State == Starting && deref(p.Attempt) == attempt {
			return nil
		}
		if p.State != Claimed {
			return fmt.Errorf("%w: admit requires CLAIMED, have %s", ErrRefused, p.State)
		}
		card := *p
		return ctrl.WithCoordinator(ctx, now, func(adm Admission) error {
			tok, err := adm.ConsumeStartToken(attempt)
			if err != nil {
				return err
			}
			job := cloneString(card.Job)
			if job == nil {
				id, err := mintID()
				if err != nil {
					return err
				}
				job = strptr(id)
			}
			lease := cloneString(card.Lease)
			if lease == nil {
				id, err := mintID()
				if err != nil {
					return err
				}
				lease = strptr(id)
			}
			nonce, err := mintID()
			if err != nil {
				return err
			}
			attest, err := mintID()
			if err != nil {
				return err
			}
			gen := tok.Generation
			ev := Event{
				Card:        card.Card,
				Attempt:     strptr(attempt),
				Prior:       strptr(Claimed),
				New:         Starting,
				Rev:         card.Rev + 1,
				Generation:  intptr(gen),
				Bench:       cloneString(card.Bench),
				Route:       cloneString(card.Route),
				Source:      cloneString(card.Source),
				Job:         job,
				Lease:       lease,
				Limits:      cloneLimits(card.Limits),
				At:          stamp(now),
				Idempotency: "starting:" + attempt,
				FenceEpoch:  card.FenceEpoch,
				Nonce:       strptr(nonce),
				ExitAttest:  strptr(attest),
			}
			return s.commit(ev)
		})
	})
}

// ApplyStarted records STARTED only from a typed, fully bound acknowledgement
// matching the identities already on the attempt. A late correct ack reconciles
// the same attempt from UNKNOWN.
func (s *Store) ApplyStarted(receipt StartedReceipt) error {
	if s == nil {
		return fmt.Errorf("lifecycle: store is nil")
	}
	if err := receipt.validate(); err != nil {
		return err
	}
	return s.mutate(func() error {
		p, ok := s.byAttempt(receipt.Attempt)
		if !ok {
			return fmt.Errorf("%w: attempt %s is not in the ledger", ErrMalformed, receipt.Attempt)
		}
		if p.Card != receipt.Card || deref(p.Attempt) != receipt.Attempt {
			return fmt.Errorf("%w: card or attempt", ErrMalformed)
		}
		if p.State == Started {
			orig, ok := s.startedEvent(receipt.Attempt)
			if !ok || !sameStarted(orig, receipt) {
				return fmt.Errorf("%w: idempotency key reused with a different payload", ErrRefused)
			}
			return writeAtomic(s.attemptFile(receipt.Attempt, startedName), []byte(receipt.Line()+"\n"))
		}
		if p.State != Starting && p.State != Unknown {
			return fmt.Errorf("%w: STARTED from %s", ErrMalformed, p.State)
		}
		if deref(p.Job) != receipt.Job || deref(p.Lease) != receipt.Lease {
			return fmt.Errorf("%w: job or lease", ErrMalformed)
		}
		if deref(p.Bench) != receipt.Bench || deref(p.Route) != receipt.Route {
			return fmt.Errorf("%w: bench or route", ErrMalformed)
		}
		if p.Generation == nil || *p.Generation != receipt.Generation {
			return fmt.Errorf("%w: generation", ErrMalformed)
		}
		ev := Event{
			Card:        p.Card,
			Attempt:     cloneString(p.Attempt),
			Prior:       strptr(p.State),
			New:         Started,
			Rev:         p.Rev + 1,
			Generation:  cloneInt(p.Generation),
			Bench:       cloneString(p.Bench),
			Route:       cloneString(p.Route),
			Source:      cloneString(p.Source),
			Job:         cloneString(p.Job),
			Lease:       cloneString(p.Lease),
			Limits:      cloneLimits(p.Limits),
			At:          stamp(receipt.At),
			Idempotency: startedKey(receipt.Attempt),
			Worker:      strptr(receipt.Worker),
			FenceEpoch:  p.FenceEpoch,
			Nonce:       cloneString(p.Nonce),
			ExitAttest:  cloneString(p.ExitAttest),
		}
		if err := s.commit(ev); err != nil {
			return err
		}
		return writeAtomic(s.attemptFile(receipt.Attempt, startedName), []byte(receipt.Line()+"\n"))
	})
}

// ApplyUnknown records UNKNOWN with raised=true and retains stdout, stderr and exit.
func (s *Store) ApplyUnknown(why UnknownWhy) error {
	if s == nil {
		return fmt.Errorf("lifecycle: store is nil")
	}
	if !validID(why.Attempt) || !validWhy(why.Why) {
		return fmt.Errorf("%w: attempt and why", ErrMalformed)
	}
	return s.mutate(func() error {
		p, ok := s.byAttempt(why.Attempt)
		if !ok {
			return fmt.Errorf("%w: attempt %s is not in the ledger", ErrRefused, why.Attempt)
		}
		if p.State != Starting && p.State != Started && p.State != Returned {
			return fmt.Errorf("%w: UNKNOWN from %s", ErrRefused, p.State)
		}
		if err := s.retainStreams(why); err != nil {
			return err
		}
		ev := Event{
			Card:        p.Card,
			Attempt:     cloneString(p.Attempt),
			Prior:       strptr(p.State),
			New:         Unknown,
			Rev:         p.Rev + 1,
			Generation:  cloneInt(p.Generation),
			Bench:       cloneString(p.Bench),
			Route:       cloneString(p.Route),
			Source:      cloneString(p.Source),
			Job:         cloneString(p.Job),
			Lease:       cloneString(p.Lease),
			Limits:      cloneLimits(p.Limits),
			At:          stamp(time.Now()),
			Idempotency: "unknown:" + why.Attempt,
			Raised:      boolptr(true),
			Reason:      strptr(why.Why),
			Worker:      cloneString(p.Worker),
			FenceEpoch:  p.FenceEpoch,
			Nonce:       cloneString(p.Nonce),
			ExitAttest:  cloneString(p.ExitAttest),
			Execution:   cloneString(p.Execution),
		}
		return s.commit(ev)
	})
}

// RecordNeverAdmitted is legal only while the last durable event is CLAIMED,
// before STARTING authorized invocation.
func (s *Store) RecordNeverAdmitted(attempt string) error {
	if s == nil {
		return fmt.Errorf("lifecycle: store is nil")
	}
	if !validID(attempt) {
		return fmt.Errorf("%w: attempt", ErrRefused)
	}
	return s.mutate(func() error {
		p, ok := s.byAttempt(attempt)
		if !ok {
			return fmt.Errorf("%w: attempt %s is not in the ledger", ErrRefused, attempt)
		}
		if p.Execution != nil && *p.Execution == ExecutionNeverAdmitted && p.State == Claimed {
			return nil
		}
		if p.State != Claimed {
			return fmt.Errorf("%w: never-admitted is legal only while CLAIMED, before STARTING (have %s)", ErrRefused, p.State)
		}
		ev := Event{
			Card:        p.Card,
			Attempt:     cloneString(p.Attempt),
			Prior:       strptr(Claimed),
			New:         Claimed,
			Rev:         p.Rev + 1,
			Generation:  cloneInt(p.Generation),
			Bench:       cloneString(p.Bench),
			Route:       cloneString(p.Route),
			Source:      cloneString(p.Source),
			Job:         cloneString(p.Job),
			Lease:       cloneString(p.Lease),
			Limits:      cloneLimits(p.Limits),
			At:          stamp(time.Now()),
			Idempotency: "never-admitted:" + attempt,
			FenceEpoch:  p.FenceEpoch,
			Nonce:       cloneString(p.Nonce),
			ExitAttest:  cloneString(p.ExitAttest),
			Execution:   strptr(ExecutionNeverAdmitted),
		}
		return s.commit(ev)
	})
}

// RecordExecutorEnded records a terminated/completed receipt authenticated by
// this launch's exit_attest and nonce. After STARTING, that pair is the only
// legal execution proof; missing job or lease files are not.
func (s *Store) RecordExecutorEnded(attempt, execution, exitAttest, nonce string) error {
	if s == nil {
		return fmt.Errorf("lifecycle: store is nil")
	}
	if execution != ExecutionTerminated && execution != ExecutionCompleted {
		return fmt.Errorf("%w: execution", ErrMalformed)
	}
	if !validID(attempt) {
		return fmt.Errorf("%w: attempt", ErrRefused)
	}
	return s.mutate(func() error {
		p, ok := s.byAttempt(attempt)
		if !ok {
			return fmt.Errorf("%w: attempt %s is not in the ledger", ErrRefused, attempt)
		}
		if p.State != Starting && p.State != Started && p.State != Unknown {
			return fmt.Errorf("%w: executor receipt from %s", ErrRefused, p.State)
		}
		if p.ExitAttest == nil || p.Nonce == nil || *p.ExitAttest != exitAttest || *p.Nonce != nonce {
			return fmt.Errorf("%w: exit_attest and nonce", ErrMalformed)
		}
		ev := Event{
			Card:        p.Card,
			Attempt:     cloneString(p.Attempt),
			Prior:       strptr(p.State),
			New:         p.State,
			Rev:         p.Rev + 1,
			Generation:  cloneInt(p.Generation),
			Bench:       cloneString(p.Bench),
			Route:       cloneString(p.Route),
			Source:      cloneString(p.Source),
			Job:         cloneString(p.Job),
			Lease:       cloneString(p.Lease),
			Limits:      cloneLimits(p.Limits),
			At:          stamp(time.Now()),
			Idempotency: "executor:" + attempt + ":" + execution,
			Raised:      boolptr(p.Raised),
			Reason:      cloneString(p.Reason),
			Worker:      cloneString(p.Worker),
			FenceEpoch:  p.FenceEpoch,
			Nonce:       cloneString(p.Nonce),
			ExitAttest:  cloneString(p.ExitAttest),
			Execution:   strptr(execution),
		}
		if err := s.commit(ev); err != nil {
			return err
		}
		body := []byte(fmt.Sprintf("execution=%s exit_attest=%s nonce=%s\n", execution, exitAttest, nonce))
		return writeAtomic(s.attemptFile(attempt, executorName), body)
	})
}

// AdvanceFence durably increments the card-level publication-fence epoch.
// Advancement alone does not make a retry legal.
func (s *Store) AdvanceFence(card string) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("lifecycle: store is nil")
	}
	if !validID(card) {
		return 0, fmt.Errorf("%w: card", ErrRefused)
	}
	var epoch int
	err := s.mutate(func() error {
		p, ok := s.cards[card]
		if !ok || p == nil {
			return fmt.Errorf("%w: card %s is not in the ledger", ErrRefused, card)
		}
		epoch = p.FenceEpoch + 1
		ev := Event{
			Card:        p.Card,
			Attempt:     cloneString(p.Attempt),
			Prior:       strptr(p.State),
			New:         p.State,
			Rev:         p.Rev + 1,
			Generation:  cloneInt(p.Generation),
			Bench:       cloneString(p.Bench),
			Route:       cloneString(p.Route),
			Source:      cloneString(p.Source),
			Job:         cloneString(p.Job),
			Lease:       cloneString(p.Lease),
			Limits:      cloneLimits(p.Limits),
			At:          stamp(time.Now()),
			Idempotency: fmt.Sprintf("fence:%s:%d", card, epoch),
			Raised:      boolptr(p.Raised),
			Reason:      cloneString(p.Reason),
			Worker:      cloneString(p.Worker),
			FenceEpoch:  epoch,
			Nonce:       cloneString(p.Nonce),
			ExitAttest:  cloneString(p.ExitAttest),
			Execution:   cloneString(p.Execution),
		}
		return s.commit(ev)
	})
	return epoch, err
}

func startedKey(attempt string) string { return "started:" + attempt }

func sameStarted(ev Event, r StartedReceipt) bool {
	gen := 0
	if ev.Generation != nil {
		gen = *ev.Generation
	}
	return ev.Card == r.Card &&
		deref(ev.Attempt) == r.Attempt &&
		deref(ev.Job) == r.Job &&
		deref(ev.Lease) == r.Lease &&
		deref(ev.Bench) == r.Bench &&
		deref(ev.Route) == r.Route &&
		gen == r.Generation &&
		deref(ev.Worker) == r.Worker &&
		ev.At == stamp(r.At)
}

func (s *Store) retainStreams(why UnknownWhy) error {
	stdout := why.Stdout
	if stdout == nil {
		stdout = []byte{}
	}
	stderr := why.Stderr
	if stderr == nil {
		stderr = []byte{}
	}
	exit := []byte("-\n")
	if why.Exit != nil {
		exit = []byte(strconv.Itoa(*why.Exit) + "\n")
	}
	if err := writeAtomic(s.attemptFile(why.Attempt, stdoutName), stdout); err != nil {
		return err
	}
	if err := writeAtomic(s.attemptFile(why.Attempt, stderrName), stderr); err != nil {
		return err
	}
	return writeAtomic(s.attemptFile(why.Attempt, exitName), exit)
}

func (s *Store) attemptFile(attempt, name string) string {
	return filepath.Join(s.attemptDir(attempt), name)
}
