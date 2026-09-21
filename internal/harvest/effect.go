package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CardAuthority provides read-only access to card projections from the lifecycle authority.
// Invariant: The effect-owner only reads this authority; it NEVER writes events.jsonl.
type CardAuthority interface {
	LookupCard(card string) (CardProjection, bool, error)
}

// ControlAuthority provides linearization and verification of fleet control state.
// WithCoordinator holds the one coordinator lock through verification and effect commitment.
type ControlAuthority interface {
	WithCoordinator(ctx context.Context, now time.Time, fn func(state ControlState) error) error
}

// EffectOwner coordinates and commits side-effects (authoritative RESULT, push, accept)
// for harvest against the lifecycle ledger authority and fleet control.
//
// Invariants (Row 3):
// 1. Verifies fence epoch + RUN action token at linearization.
// 2. Does NOT write events.jsonl (admission authority writes that).
type EffectOwner struct {
	cards CardAuthority
	ctrl  ControlAuthority
}

// NewEffectOwner creates an EffectOwner wired to a CardAuthority and ControlAuthority.
func NewEffectOwner(cards CardAuthority, ctrl ControlAuthority) *EffectOwner {
	return &EffectOwner{
		cards: cards,
		ctrl:  ctrl,
	}
}

// VerifyAtLinearization verifies the presented attempt credentials and RUN action token
// under the control authority's coordinator lock.
//
// It verifies:
// - Card exists in lifecycle authority.
// - Attempt matches the card's active claimant.
// - Attempt's fence epoch matches the card's current fence epoch (refuses stale workers).
// - Fleet control is currently in desired RUN state.
// - Action token is valid, matches card, attempt, action, fleet scope, and current RUN generation.
// - Neither the fleet control state nor the action token has expired at `now`.
//
// Crucially: This method does NOT write events.jsonl.
func (e *EffectOwner) VerifyAtLinearization(ctx context.Context, now time.Time, creds AttemptCredentials, action ActionType, token ActionToken) error {
	if e == nil {
		return fmt.Errorf("harvest: nil EffectOwner")
	}
	if e.ctrl == nil {
		return fmt.Errorf("harvest: nil ControlAuthority")
	}
	if e.cards == nil {
		return fmt.Errorf("harvest: nil CardAuthority")
	}

	return e.ctrl.WithCoordinator(ctx, now, func(ctrlState ControlState) error {
		return e.verifyInternal(now, ctrlState, creds, action, token)
	})
}

// CommitEffect verifies the credentials and action token at linearization, and if valid,
// executes the side-effect callback while the coordinator lock is held.
//
// Crucially: Does NOT write events.jsonl.
func (e *EffectOwner) CommitEffect(ctx context.Context, now time.Time, creds AttemptCredentials, action ActionType, token ActionToken, effect func() error) error {
	if e == nil {
		return fmt.Errorf("harvest: nil EffectOwner")
	}
	if e.ctrl == nil {
		return fmt.Errorf("harvest: nil ControlAuthority")
	}
	if e.cards == nil {
		return fmt.Errorf("harvest: nil CardAuthority")
	}

	return e.ctrl.WithCoordinator(ctx, now, func(ctrlState ControlState) error {
		if err := e.verifyInternal(now, ctrlState, creds, action, token); err != nil {
			return err
		}
		if effect != nil {
			return effect()
		}
		return nil
	})
}

// verifyInternal performs the validation checks within the linearization boundary.
func (e *EffectOwner) verifyInternal(now time.Time, ctrlState ControlState, creds AttemptCredentials, action ActionType, token ActionToken) error {
	// 1. Validate control state: must be RUN and unexpired.
	if strings.ToUpper(strings.TrimSpace(ctrlState.Desired)) != "RUN" {
		return fmt.Errorf("%w (desired=%s)", ErrNotRun, ctrlState.Desired)
	}
	if !ctrlState.Expires.IsZero() && !now.Before(ctrlState.Expires) {
		return fmt.Errorf("%w: fleet control expired at %s (now=%s)",
			ErrTokenExpired, ctrlState.Expires.Format(time.RFC3339), now.Format(time.RFC3339))
	}

	// 2. Validate token presence and binding.
	if strings.TrimSpace(token.TokenID) == "" {
		return ErrMissingToken
	}
	if token.Card != creds.Card {
		return fmt.Errorf("%w: token card %q != creds card %q", ErrCardMismatch, token.Card, creds.Card)
	}
	if token.Attempt != creds.Attempt {
		return fmt.Errorf("%w: token attempt %q != creds attempt %q", ErrAttemptMismatch, token.Attempt, creds.Attempt)
	}
	if token.Action != action {
		return fmt.Errorf("%w: token action %q != required action %q", ErrActionMismatch, token.Action, action)
	}
	if token.Scope != "" && token.Scope != "fleet" {
		return fmt.Errorf("%w: token scope %q != fleet", ErrScopeMismatch, token.Scope)
	}
	if token.Generation != ctrlState.Generation {
		return fmt.Errorf("%w: token gen %d != current gen %d",
			ErrGenerationMismatch, token.Generation, ctrlState.Generation)
	}
	if !token.Expires.IsZero() && !now.Before(token.Expires) {
		return fmt.Errorf("%w: action token expired at %s (now=%s)",
			ErrTokenExpired, token.Expires.Format(time.RFC3339), now.Format(time.RFC3339))
	}

	// 3. Validate against lifecycle card authority: fence epoch and attempt binding.
	proj, exists, err := e.cards.LookupCard(creds.Card)
	if err != nil {
		return fmt.Errorf("harvest: lookup card %s: %w", creds.Card, err)
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrCardNotFound, creds.Card)
	}
	if proj.Attempt != "" && proj.Attempt != creds.Attempt {
		return fmt.Errorf("%w: creds attempt %q != active attempt %q",
			ErrMismatchedAttempt, creds.Attempt, proj.Attempt)
	}
	if proj.FenceEpoch != creds.FenceEpoch {
		return fmt.Errorf("%w: creds epoch %d != card epoch %d",
			ErrStaleFence, creds.FenceEpoch, proj.FenceEpoch)
	}

	return nil
}

// DiskCardAuthority reads card projections from `<lifecycleDir>/cards/<card>.json`.
// It strictly performs reads and NEVER writes to events.jsonl.
type DiskCardAuthority struct {
	lifecycleDir string
}

// NewDiskCardAuthority creates a DiskCardAuthority reading from the given lifecycle directory.
func NewDiskCardAuthority(lifecycleDir string) *DiskCardAuthority {
	return &DiskCardAuthority{lifecycleDir: lifecycleDir}
}

func (d *DiskCardAuthority) LookupCard(card string) (CardProjection, bool, error) {
	path := filepath.Join(d.lifecycleDir, "cards", card+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CardProjection{}, false, nil
		}
		return CardProjection{}, false, err
	}
	var raw struct {
		Card       string  `json:"card"`
		Attempt    *string `json:"attempt"`
		Fence      int     `json:"fence"`
		FenceEpoch int     `json:"fence_epoch"`
		State      string  `json:"state"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CardProjection{}, false, fmt.Errorf("decode card projection %s: %w", path, err)
	}

	epoch := raw.FenceEpoch
	if epoch == 0 && raw.Fence != 0 {
		epoch = raw.Fence
	}
	attempt := ""
	if raw.Attempt != nil {
		attempt = *raw.Attempt
	}
	return CardProjection{
		Card:       raw.Card,
		Attempt:    attempt,
		FenceEpoch: epoch,
		State:      raw.State,
	}, true, nil
}

// MemoryCardAuthority stores card projections in memory. Useful for fakes and unit tests.
// Invariant: Never writes events.jsonl.
type MemoryCardAuthority struct {
	mu    sync.RWMutex
	cards map[string]CardProjection
}

// NewMemoryCardAuthority creates an empty in-memory CardAuthority.
func NewMemoryCardAuthority() *MemoryCardAuthority {
	return &MemoryCardAuthority{
		cards: make(map[string]CardProjection),
	}
}

func (m *MemoryCardAuthority) Set(proj CardProjection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cards[proj.Card] = proj
}

func (m *MemoryCardAuthority) AdvanceFence(card string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proj, ok := m.cards[card]
	if !ok {
		return 0, ErrCardNotFound
	}
	proj.FenceEpoch++
	m.cards[card] = proj
	return proj.FenceEpoch, nil
}

func (m *MemoryCardAuthority) LookupCard(card string) (CardProjection, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proj, ok := m.cards[card]
	return proj, ok, nil
}

// FakeControl linearizes execution and models fleet control RUN/PAUSE state.
type FakeControl struct {
	mu         sync.Mutex
	desired    string
	generation int
	expires    time.Time
	scope      string
}

// NewFakeControl creates a FakeControl initialized to RUN at the given generation.
func NewFakeControl(gen int, expires time.Time) *FakeControl {
	return &FakeControl{
		desired:    "RUN",
		generation: gen,
		expires:    expires,
		scope:      "fleet",
	}
}

func (f *FakeControl) WithCoordinator(ctx context.Context, now time.Time, fn func(state ControlState) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ControlState{
		Generation: f.generation,
		Desired:    f.desired,
		Expires:    f.expires,
		Scope:      f.scope,
	})
}

func (f *FakeControl) Pause() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.desired = "PAUSE"
}

func (f *FakeControl) Resume(now time.Time, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.desired = "RUN"
	f.expires = now.Add(d)
}

func (f *FakeControl) SetGeneration(gen int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation = gen
}
