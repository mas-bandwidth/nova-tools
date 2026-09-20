// Package harvest is the SPEC-PULSE effect owner: it verifies the card's
// publication-fence epoch and a bounded RUN action token at its own
// linearization. It does not write lifecycle/events.jsonl.
package harvest

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Action is one publication effect. RESULT, harvest push and accept each need
// their own RUN action token.
type Action string

const (
	ActionRESULT Action = "RESULT"
	ActionPush   Action = "push"
	ActionAccept Action = "accept"
)

// Credential is the attempt ownership a worker presents.
type Credential struct {
	Card       string
	Attempt    string
	FenceEpoch int
}

// Token is a bounded RUN action token acquired after checking the current
// control generation. There is no timeless cached RUN permission.
type Token struct {
	ID         string
	Card       string
	Attempt    string
	Action     Action
	Generation int
	Scope      string
	Expires    time.Time
}

var (
	ErrStaleFence = errors.New("harvest: stale fence epoch")
	ErrAttempt    = errors.New("harvest: attempt is not the card's current claimant")
	ErrNotRun     = errors.New("harvest: fleet control is not RUN")
	ErrToken      = errors.New("harvest: missing or expired RUN action token")
	ErrGeneration = errors.New("harvest: action token generation is not current")
	ErrAction     = errors.New("harvest: action token is for a different effect")
	ErrCard       = errors.New("harvest: card is not in the lifecycle projection")
	ErrScope      = errors.New("harvest: action token scope is not fleet")
	ErrMismatch   = errors.New("harvest: action token is bound to a different card or attempt")
)

// View is the coordinator-held control snapshot inside WithCoordinator.
type View interface {
	Desired() string
	Generation() int
	Expires() time.Time
}

// Control linearizes RUN vs PAUSE at the effect owner's commit. This is not
// internal/control: that store is a different slice.
type Control interface {
	WithCoordinator(ctx context.Context, now time.Time, fn func(View) error) error
}

// Owner verifies fence epoch and RUN action token, then runs the effect.
type Owner struct {
	cards Cards
	ctrl  Control
}

// New wires a read-only card projection to a control linearizer.
func New(cards Cards, ctrl Control) *Owner {
	return &Owner{cards: cards, ctrl: ctrl}
}

// Commit verifies credential, token and the card's current fence epoch under
// the coordinator lock, then runs effect. It does not append events.jsonl.
func (o *Owner) Commit(ctx context.Context, now time.Time, cred Credential, action Action, token Token, effect func() error) error {
	if o == nil || o.cards == nil || o.ctrl == nil {
		return fmt.Errorf("harvest: owner is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return o.ctrl.WithCoordinator(ctx, now, func(v View) error {
		if err := check(now, v, o.cards, cred, action, token); err != nil {
			return err
		}
		if effect != nil {
			return effect()
		}
		return nil
	})
}

func check(now time.Time, v View, cards Cards, cred Credential, action Action, token Token) error {
	if token.ID == "" {
		return ErrToken
	}
	if token.Action != action {
		return fmt.Errorf("%w: token %s required %s", ErrAction, token.Action, action)
	}
	if token.Card != cred.Card || token.Attempt != cred.Attempt {
		return fmt.Errorf("%w: token card=%s attempt=%s cred card=%s attempt=%s", ErrMismatch, token.Card, token.Attempt, cred.Card, cred.Attempt)
	}
	if token.Scope != "" && token.Scope != "fleet" {
		return fmt.Errorf("%w: %s", ErrScope, token.Scope)
	}
	if v.Desired() != "RUN" {
		return fmt.Errorf("%w (desired=%s)", ErrNotRun, v.Desired())
	}
	if v.Expires().IsZero() || !now.Before(v.Expires()) {
		return fmt.Errorf("%w: control expired", ErrToken)
	}
	if token.Generation != v.Generation() {
		return fmt.Errorf("%w: token %d current %d", ErrGeneration, token.Generation, v.Generation())
	}
	if token.Expires.IsZero() || !now.Before(token.Expires) {
		return fmt.Errorf("%w: action token expired", ErrToken)
	}
	proj, ok, err := cards.Lookup(cred.Card)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrCard, cred.Card)
	}
	if cred.FenceEpoch != proj.FenceEpoch {
		return fmt.Errorf("%w: presented %d card %d", ErrStaleFence, cred.FenceEpoch, proj.FenceEpoch)
	}
	if cred.Attempt != proj.Attempt {
		return fmt.Errorf("%w: presented %s card %s", ErrAttempt, cred.Attempt, proj.Attempt)
	}
	return nil
}
