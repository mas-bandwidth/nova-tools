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

// TokenAuthority provides authoritative issuance verification, durable outcome recording,
// and retry reconciliation for RUN action tokens under the coordinator lock.
//
// Invariants (SPEC-PULSE lines 1505-1508, 1593-1598):
// 1. Tokens must be issued by the authoritative store (rejects forged/self-asserted tokens).
// 2. Reusing a completed token is rejected as a replay attack.
// 3. Retrying an action reconciles the existing token outcome rather than permitting duplicate commits.
type TokenAuthority interface {
	// LookupToken retrieves the authoritative token by tokenID.
	// Returns (AuthoritativeToken{}, false, nil) if the token was never issued by the authority.
	LookupToken(tokenID string) (AuthoritativeToken, bool, error)

	// RecordOutcome durably records the result of committing an action with tokenID.
	RecordOutcome(tokenID string, state TokenState, outcomeErr error) error
}

// EffectOwner coordinates and commits side-effects (authoritative RESULT, push, accept)
// for harvest against the lifecycle ledger authority and fleet control.
//
// Invariants (Row 3):
// 1. Verifies fence epoch + RUN action token at linearization against authoritative stores.
// 2. Does NOT write events.jsonl (admission authority writes that).
// 3. Durably records and reconciles action token outcomes to prevent replay.
type EffectOwner struct {
	cards  CardAuthority
	ctrl   ControlAuthority
	tokens TokenAuthority
}

// NewEffectOwner creates an EffectOwner wired to a CardAuthority, ControlAuthority, and optional TokenAuthority.
func NewEffectOwner(cards CardAuthority, ctrl ControlAuthority, tokens ...TokenAuthority) *EffectOwner {
	var tokAuth TokenAuthority
	if len(tokens) > 0 {
		tokAuth = tokens[0]
	}
	return &EffectOwner{
		cards:  cards,
		ctrl:   ctrl,
		tokens: tokAuth,
	}
}

// TokenAuthority returns the configured TokenAuthority, if any.
func (e *EffectOwner) TokenAuthority() TokenAuthority {
	if e == nil {
		return nil
	}
	return e.tokens
}

// VerifyAtLinearization verifies the presented attempt credentials and RUN action token
// under the control authority's coordinator lock.
//
// It verifies:
// - Card exists in lifecycle authority.
// - Attempt matches the card's active claimant.
// - Attempt's fence epoch matches the card's current fence epoch (refuses stale workers).
// - Fleet control is currently in desired RUN state.
// - Action token was authoritatively issued (not forged or self-asserted).
// - Action token has not already completed (no replay).
// - Action token matches card, attempt, action, fleet scope, and current RUN generation.
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

// CommitEffect verifies the credentials and action token at linearization, executes
// the side-effect callback while the coordinator lock is held, and durably records
// the outcome on the token authority.
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
			if err := effect(); err != nil {
				if e.tokens != nil {
					_ = e.tokens.RecordOutcome(token.TokenID, TokenFailed, err)
				}
				return err
			}
		}
		if e.tokens != nil {
			return e.tokens.RecordOutcome(token.TokenID, TokenCompleted, nil)
		}
		return nil
	})
}

// ReconcileToken checks the recorded outcome of an action token during retry reconciliation.
// SPEC-PULSE lines 1595-1598: "Retrying such an action reconciles the same token rather
// than acquiring a new one. The result names whether the effect completed, failed or is unknown."
func (e *EffectOwner) ReconcileToken(ctx context.Context, now time.Time, tokenID string) (AuthoritativeToken, error) {
	if e == nil {
		return AuthoritativeToken{}, fmt.Errorf("harvest: nil EffectOwner")
	}
	if e.tokens == nil {
		return AuthoritativeToken{}, ErrNoTokenAuthority
	}
	if e.ctrl == nil {
		return AuthoritativeToken{}, fmt.Errorf("harvest: nil ControlAuthority")
	}
	var res AuthoritativeToken
	err := e.ctrl.WithCoordinator(ctx, now, func(ctrlState ControlState) error {
		tok, ok, err := e.tokens.LookupToken(tokenID)
		if err != nil {
			return fmt.Errorf("harvest: reconcile token lookup %s: %w", tokenID, err)
		}
		if !ok {
			return fmt.Errorf("%w: token %q not found in authoritative store", ErrForgedToken, tokenID)
		}
		res = tok
		return nil
	})
	return res, err
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

	// 2. Validate token presence, authority issuance, and binding.
	if strings.TrimSpace(token.TokenID) == "" {
		return ErrMissingToken
	}
	if e.tokens == nil {
		return ErrNoTokenAuthority
	}

	authTok, exists, err := e.tokens.LookupToken(token.TokenID)
	if err != nil {
		return fmt.Errorf("harvest: token authority lookup %s: %w", token.TokenID, err)
	}
	if !exists {
		return fmt.Errorf("%w: token %q not found in authoritative store", ErrForgedToken, token.TokenID)
	}

	// Replay check: Completed tokens cannot authorize a new side-effect.
	if authTok.State == TokenCompleted {
		return fmt.Errorf("%w: token %q already completed", ErrReplayedToken, token.TokenID)
	}

	// Validate card binding against both creds and authoritative issuance.
	if authTok.Card != token.Card {
		return fmt.Errorf("%w: %w: presented token card %q does not match authoritative card %q",
			ErrForgedToken, ErrCardMismatch, token.Card, authTok.Card)
	}
	if token.Card != creds.Card {
		return fmt.Errorf("%w: token card %q != creds card %q", ErrCardMismatch, token.Card, creds.Card)
	}

	// Validate attempt binding against both creds and authoritative issuance.
	if authTok.Attempt != token.Attempt {
		return fmt.Errorf("%w: %w: presented token attempt %q does not match authoritative attempt %q",
			ErrForgedToken, ErrAttemptMismatch, token.Attempt, authTok.Attempt)
	}
	if token.Attempt != creds.Attempt {
		return fmt.Errorf("%w: token attempt %q != creds attempt %q", ErrAttemptMismatch, token.Attempt, creds.Attempt)
	}

	// Validate action type against both requested action and authoritative issuance.
	if authTok.Action != token.Action {
		return fmt.Errorf("%w: %w: presented token action %q does not match authoritative action %q",
			ErrForgedToken, ErrActionMismatch, token.Action, authTok.Action)
	}
	if token.Action != action {
		return fmt.Errorf("%w: token action %q != required action %q", ErrActionMismatch, token.Action, action)
	}

	// Validate generation against both control state and authoritative issuance.
	if authTok.Generation != token.Generation {
		return fmt.Errorf("%w: %w: presented token gen %d does not match authoritative gen %d",
			ErrForgedToken, ErrGenerationMismatch, token.Generation, authTok.Generation)
	}
	if token.Generation != ctrlState.Generation {
		return fmt.Errorf("%w: token gen %d != current gen %d",
			ErrGenerationMismatch, token.Generation, ctrlState.Generation)
	}

	// Validate scope against fleet and authoritative issuance.
	if authTok.Scope != "" && token.Scope != "" && authTok.Scope != token.Scope {
		return fmt.Errorf("%w: %w: presented token scope %q does not match authoritative scope %q",
			ErrForgedToken, ErrScopeMismatch, token.Scope, authTok.Scope)
	}
	if token.Scope != "" && token.Scope != "fleet" {
		return fmt.Errorf("%w: token scope %q != fleet", ErrScopeMismatch, token.Scope)
	}

	if !token.Expires.IsZero() && !now.Before(token.Expires) {
		return fmt.Errorf("%w: action token expired at %s (now=%s)",
			ErrTokenExpired, token.Expires.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if !authTok.Expires.IsZero() && !now.Before(authTok.Expires) {
		return fmt.Errorf("%w: authoritative token expired at %s (now=%s)",
			ErrTokenExpired, authTok.Expires.Format(time.RFC3339), now.Format(time.RFC3339))
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

// MemoryTokenAuthority stores authoritative action tokens in memory.
// Useful for fakes, simulation, and unit tests.
type MemoryTokenAuthority struct {
	mu     sync.RWMutex
	tokens map[string]AuthoritativeToken
}

// NewMemoryTokenAuthority creates an empty in-memory TokenAuthority.
func NewMemoryTokenAuthority() *MemoryTokenAuthority {
	return &MemoryTokenAuthority{
		tokens: make(map[string]AuthoritativeToken),
	}
}

// IssueToken inserts or updates an authoritative token in the memory authority.
func (m *MemoryTokenAuthority) IssueToken(tok AuthoritativeToken) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tok.State == "" {
		tok.State = TokenIssued
	}
	m.tokens[tok.TokenID] = tok
}

// IssueActionToken converts an ActionToken into an AuthoritativeToken in TokenIssued state.
func (m *MemoryTokenAuthority) IssueActionToken(tok ActionToken) {
	m.IssueToken(AuthoritativeToken{
		TokenID:    tok.TokenID,
		Card:       tok.Card,
		Attempt:    tok.Attempt,
		Action:     tok.Action,
		Generation: tok.Generation,
		Scope:      tok.Scope,
		Expires:    tok.Expires,
		State:      TokenIssued,
	})
}

// LookupToken retrieves the authoritative token by tokenID.
func (m *MemoryTokenAuthority) LookupToken(tokenID string) (AuthoritativeToken, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tok, ok := m.tokens[tokenID]
	return tok, ok, nil
}

// RecordOutcome updates the token's outcome state in memory.
func (m *MemoryTokenAuthority) RecordOutcome(tokenID string, state TokenState, outcomeErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tok, ok := m.tokens[tokenID]
	if !ok {
		return fmt.Errorf("%w: token %s", ErrForgedToken, tokenID)
	}
	tok.State = state
	if outcomeErr != nil {
		tok.OutcomeErr = outcomeErr.Error()
	} else {
		tok.OutcomeErr = ""
	}
	m.tokens[tokenID] = tok
	return nil
}

// DiskTokenAuthority stores and validates authoritative action tokens under `<controlDir>/tokens/<tokenID>.json`.
// Invariant: Tokens must be issued into this store before use; outcomes are persisted to prevent replay.
type DiskTokenAuthority struct {
	controlDir string
	mu         sync.Mutex
}

// NewDiskTokenAuthority creates a DiskTokenAuthority reading/writing under the given control directory.
func NewDiskTokenAuthority(controlDir string) *DiskTokenAuthority {
	return &DiskTokenAuthority{controlDir: controlDir}
}

func (d *DiskTokenAuthority) tokenPath(tokenID string) string {
	return filepath.Join(d.controlDir, "tokens", tokenID+".json")
}

// LookupToken reads the authoritative token from disk.
func (d *DiskTokenAuthority) LookupToken(tokenID string) (AuthoritativeToken, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	path := d.tokenPath(tokenID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AuthoritativeToken{}, false, nil
		}
		return AuthoritativeToken{}, false, err
	}
	var tok AuthoritativeToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return AuthoritativeToken{}, false, fmt.Errorf("decode action token %s: %w", path, err)
	}
	return tok, true, nil
}

// IssueToken writes an authoritative token to `<controlDir>/tokens/<tokenID>.json`.
func (d *DiskTokenAuthority) IssueToken(tok AuthoritativeToken) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tokensDir := filepath.Join(d.controlDir, "tokens")
	if err := os.MkdirAll(tokensDir, 0o755); err != nil {
		return err
	}
	if tok.State == "" {
		tok.State = TokenIssued
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	path := d.tokenPath(tok.TokenID)
	return os.WriteFile(path, data, 0o644)
}

// IssueActionToken converts an ActionToken into an AuthoritativeToken in TokenIssued state and persists it.
func (d *DiskTokenAuthority) IssueActionToken(tok ActionToken) error {
	return d.IssueToken(AuthoritativeToken{
		TokenID:    tok.TokenID,
		Card:       tok.Card,
		Attempt:    tok.Attempt,
		Action:     tok.Action,
		Generation: tok.Generation,
		Scope:      tok.Scope,
		Expires:    tok.Expires,
		State:      TokenIssued,
	})
}

// RecordOutcome updates the token's outcome state on disk.
func (d *DiskTokenAuthority) RecordOutcome(tokenID string, state TokenState, outcomeErr error) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	path := d.tokenPath(tokenID)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var tok AuthoritativeToken
	if err := json.Unmarshal(data, &tok); err != nil {
		return fmt.Errorf("decode action token %s: %w", path, err)
	}
	tok.State = state
	if outcomeErr != nil {
		tok.OutcomeErr = outcomeErr.Error()
	} else {
		tok.OutcomeErr = ""
	}
	updated, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0o644)
}
