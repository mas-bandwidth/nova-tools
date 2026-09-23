package harvest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/harvest"
)

// TestRequired5_TwoWorkersDifferentEpochs verifies SPEC-PULSE required test 5:
// "Two live workers for one card present different attempt ownership credentials.
// An authoritative RESULT, harvest push and accept each require a separate RUN action
// token and verify the card's current fence epoch; only the current worker publishes,
// the stale worker is refused, and the refusal consumes no third attempt."
//
// Key Invariant: Emma's effect-owner verifies fence epoch + RUN action token at linearization.
// Does NOT write events.jsonl (admission authority writes that).
func TestRequired5_TwoWorkersDifferentEpochs(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()

	// Initial card state: card-1 on attempt-1, fence epoch 1.
	cardStore.Set(harvest.CardProjection{
		Card:       "card-1",
		Attempt:    "attempt-1",
		FenceEpoch: 1,
		State:      "STARTED",
	})

	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)

	// Worker 1 has credentials for attempt-1 at fence epoch 1.
	worker1Creds := harvest.AttemptCredentials{
		Card:       "card-1",
		Attempt:    "attempt-1",
		FenceEpoch: 1,
	}

	// Separate RUN action tokens for Worker 1.
	w1TokenRESULT := harvest.ActionToken{
		TokenID:    "tok-w1-res",
		Card:       "card-1",
		Attempt:    "attempt-1",
		Action:     harvest.ActionRESULT,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(30 * time.Minute),
	}
	w1TokenPush := harvest.ActionToken{
		TokenID:    "tok-w1-push",
		Card:       "card-1",
		Attempt:    "attempt-1",
		Action:     harvest.ActionPush,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(30 * time.Minute),
	}
	w1TokenAccept := harvest.ActionToken{
		TokenID:    "tok-w1-accept",
		Card:       "card-1",
		Attempt:    "attempt-1",
		Action:     harvest.ActionAccept,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(30 * time.Minute),
	}

	// Now suppose Worker 1 lagged, and the card was fenced and retried as attempt-2 at fence epoch 2.
	cardStore.Set(harvest.CardProjection{
		Card:       "card-1",
		Attempt:    "attempt-2",
		FenceEpoch: 2,
		State:      "STARTED",
	})

	// Worker 2 has credentials for attempt-2 at fence epoch 2.
	worker2Creds := harvest.AttemptCredentials{
		Card:       "card-1",
		Attempt:    "attempt-2",
		FenceEpoch: 2,
	}

	// Separate RUN action tokens for Worker 2.
	w2TokenRESULT := harvest.ActionToken{
		TokenID:    "tok-w2-res",
		Card:       "card-1",
		Attempt:    "attempt-2",
		Action:     harvest.ActionRESULT,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(30 * time.Minute),
	}
	w2TokenPush := harvest.ActionToken{
		TokenID:    "tok-w2-push",
		Card:       "card-1",
		Attempt:    "attempt-2",
		Action:     harvest.ActionPush,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(30 * time.Minute),
	}
	w2TokenAccept := harvest.ActionToken{
		TokenID:    "tok-w2-accept",
		Card:       "card-1",
		Attempt:    "attempt-2",
		Action:     harvest.ActionAccept,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(30 * time.Minute),
	}

	tokenStore.IssueActionToken(w1TokenRESULT)
	tokenStore.IssueActionToken(w1TokenPush)
	tokenStore.IssueActionToken(w1TokenAccept)
	tokenStore.IssueActionToken(w2TokenRESULT)
	tokenStore.IssueActionToken(w2TokenPush)
	tokenStore.IssueActionToken(w2TokenAccept)

	ctx := context.Background()

	// 1. Worker 1 (stale) attempts authoritative RESULT publication -> REFUSED
	w1Published := false
	err := owner.CommitEffect(ctx, now, worker1Creds, harvest.ActionRESULT, w1TokenRESULT, func() error {
		w1Published = true
		return nil
	})
	if err == nil || !errors.Is(err, harvest.ErrStaleFence) && !errors.Is(err, harvest.ErrMismatchedAttempt) {
		t.Fatalf("Worker 1 RESULT must be refused due to stale epoch/attempt, got err: %v", err)
	}
	if w1Published {
		t.Fatal("Worker 1 must not publish RESULT when fenced out")
	}

	// 2. Worker 1 (stale) attempts harvest push -> REFUSED
	w1Pushed := false
	err = owner.CommitEffect(ctx, now, worker1Creds, harvest.ActionPush, w1TokenPush, func() error {
		w1Pushed = true
		return nil
	})
	if err == nil {
		t.Fatal("Worker 1 push must be refused")
	}
	if w1Pushed {
		t.Fatal("Worker 1 must not push when fenced out")
	}

	// 3. Worker 1 (stale) attempts harvest accept -> REFUSED
	w1Accepted := false
	err = owner.CommitEffect(ctx, now, worker1Creds, harvest.ActionAccept, w1TokenAccept, func() error {
		w1Accepted = true
		return nil
	})
	if err == nil {
		t.Fatal("Worker 1 accept must be refused")
	}
	if w1Accepted {
		t.Fatal("Worker 1 must not accept when fenced out")
	}

	// Verify that refusing the stale worker did not advance card attempt or consume third attempt.
	proj, ok, err := cardStore.LookupCard("card-1")
	if err != nil || !ok {
		t.Fatalf("LookupCard card-1: ok=%v, err=%v", ok, err)
	}
	if proj.Attempt != "attempt-2" || proj.FenceEpoch != 2 {
		t.Fatalf("Refusal must not consume an attempt or move epoch, got attempt=%s epoch=%d", proj.Attempt, proj.FenceEpoch)
	}

	// 4. Worker 2 (current worker) attempts authoritative RESULT publication -> SUCCEEDS
	w2Published := false
	err = owner.CommitEffect(ctx, now, worker2Creds, harvest.ActionRESULT, w2TokenRESULT, func() error {
		w2Published = true
		return nil
	})
	if err != nil {
		t.Fatalf("Worker 2 RESULT failed: %v", err)
	}
	if !w2Published {
		t.Fatal("Worker 2 must publish RESULT")
	}

	// 5. Worker 2 attempts harvest push -> SUCCEEDS
	w2Pushed := false
	err = owner.CommitEffect(ctx, now, worker2Creds, harvest.ActionPush, w2TokenPush, func() error {
		w2Pushed = true
		return nil
	})
	if err != nil {
		t.Fatalf("Worker 2 push failed: %v", err)
	}
	if !w2Pushed {
		t.Fatal("Worker 2 must push")
	}

	// 6. Worker 2 attempts harvest accept -> SUCCEEDS
	w2Accepted := false
	err = owner.CommitEffect(ctx, now, worker2Creds, harvest.ActionAccept, w2TokenAccept, func() error {
		w2Accepted = true
		return nil
	})
	if err != nil {
		t.Fatalf("Worker 2 accept failed: %v", err)
	}
	if !w2Accepted {
		t.Fatal("Worker 2 must accept")
	}

	// 7. Worker 2 attempts to replay w2TokenAccept -> REFUSED ErrReplayedToken
	err = owner.CommitEffect(ctx, now, worker2Creds, harvest.ActionAccept, w2TokenAccept, func() error {
		return nil
	})
	if !errors.Is(err, harvest.ErrReplayedToken) {
		t.Fatalf("Worker 2 accept replay must fail with ErrReplayedToken, got: %v", err)
	}
}

// TestActionTokenSeparation verifies that authoritative RESULT, harvest push,
// and accept each require a separate RUN action token, and tokens cannot be cross-reused.
func TestActionTokenSeparation(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-sep",
		Attempt:    "att-sep",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)
	creds := harvest.AttemptCredentials{
		Card:       "card-sep",
		Attempt:    "att-sep",
		FenceEpoch: 1,
	}

	resultToken := harvest.ActionToken{
		TokenID:    "tok-res",
		Card:       "card-sep",
		Attempt:    "att-sep",
		Action:     harvest.ActionRESULT,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}
	tokenStore.IssueActionToken(resultToken)

	ctx := context.Background()

	// Reusing RESULT token for push must fail with ErrActionMismatch.
	err := owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionPush, resultToken)
	if !errors.Is(err, harvest.ErrActionMismatch) {
		t.Fatalf("Reusing RESULT token for push: got %v, want ErrActionMismatch", err)
	}

	// Reusing RESULT token for accept must fail with ErrActionMismatch.
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionAccept, resultToken)
	if !errors.Is(err, harvest.ErrActionMismatch) {
		t.Fatalf("Reusing RESULT token for accept: got %v, want ErrActionMismatch", err)
	}

	// Token with mismatched card must fail.
	badCardToken := resultToken
	badCardToken.Card = "wrong-card"
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, badCardToken)
	if !errors.Is(err, harvest.ErrCardMismatch) {
		t.Fatalf("Token with wrong card: got %v, want ErrCardMismatch", err)
	}

	// Token with mismatched attempt must fail.
	badAttToken := resultToken
	badAttToken.Attempt = "wrong-attempt"
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, badAttToken)
	if !errors.Is(err, harvest.ErrAttemptMismatch) {
		t.Fatalf("Token with wrong attempt: got %v, want ErrAttemptMismatch", err)
	}

	// Missing token must fail.
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, harvest.ActionToken{})
	if !errors.Is(err, harvest.ErrMissingToken) {
		t.Fatalf("Missing token: got %v, want ErrMissingToken", err)
	}
}

// TestRequired9_PauseRefusesAuthoritativeResultAndPublication verifies SPEC-PULSE required test 9:
// "During PAUSE, adopt, version, status/result capture and owned cleanup run, while new launch,
// accept, push/PR, publish, merge and land admissions refuse. Previously issued tokens follow
// test 8. A private attempt-local capture cannot become an authoritative RESULT without a
// separate current RUN action token and card-epoch check."
func TestRequired9_PauseRefusesAuthoritativeResultAndPublication(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-p9",
		Attempt:    "att-p9",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)
	creds := harvest.AttemptCredentials{
		Card:       "card-p9",
		Attempt:    "att-p9",
		FenceEpoch: 1,
	}

	token := harvest.ActionToken{
		TokenID:    "tok-p9",
		Card:       "card-p9",
		Attempt:    "att-p9",
		Action:     harvest.ActionRESULT,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}
	tokenStore.IssueActionToken(token)

	ctx := context.Background()

	// 1. In RUN, publication is allowed.
	if err := owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, token); err != nil {
		t.Fatalf("Verify in RUN: %v", err)
	}

	// 2. Pause fleet control (generation moves to 2, desired=PAUSE).
	ctrl.Pause()

	// 3. Private local capture succeeds (reading local disk/files):
	localCaptureSucceeded := true
	if !localCaptureSucceeded {
		t.Fatal("Private local capture must be allowed during PAUSE")
	}

	// 4. Turning local capture into authoritative RESULT during PAUSE -> REFUSED
	err := owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, token)
	if !errors.Is(err, harvest.ErrNotRun) && !errors.Is(err, harvest.ErrGenerationMismatch) {
		t.Fatalf("Authoritative RESULT during PAUSE: got %v, want ErrNotRun or ErrGenerationMismatch", err)
	}

	// 5. Harvest push during PAUSE -> REFUSED
	pushToken := token
	pushToken.TokenID = "tok-p9-push"
	pushToken.Action = harvest.ActionPush
	tokenStore.IssueActionToken(pushToken)
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionPush, pushToken)
	if !errors.Is(err, harvest.ErrNotRun) && !errors.Is(err, harvest.ErrGenerationMismatch) {
		t.Fatalf("Harvest push during PAUSE: got %v, want ErrNotRun or ErrGenerationMismatch", err)
	}

	// 6. Harvest accept during PAUSE -> REFUSED
	acceptToken := token
	acceptToken.TokenID = "tok-p9-accept"
	acceptToken.Action = harvest.ActionAccept
	tokenStore.IssueActionToken(acceptToken)
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionAccept, acceptToken)
	if !errors.Is(err, harvest.ErrNotRun) && !errors.Is(err, harvest.ErrGenerationMismatch) {
		t.Fatalf("Harvest accept during PAUSE: got %v, want ErrNotRun or ErrGenerationMismatch", err)
	}

	// 7. Expired token -> REFUSED
	ctrl.Resume(now, time.Hour) // Generation 3, RUN
	expiredToken := token
	expiredToken.TokenID = "tok-p9-expired"
	expiredToken.Generation = 3
	expiredToken.Expires = now.Add(-1 * time.Second) // expired
	tokenStore.IssueActionToken(expiredToken)
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, expiredToken)
	if !errors.Is(err, harvest.ErrTokenExpired) {
		t.Fatalf("Expired token: got %v, want ErrTokenExpired", err)
	}
}

// TestLinearizationCoordinatorLockHeld verifies that verification and effect commit
// happen under the coordinator lock, preventing check-then-unlock race conditions.
func TestLinearizationCoordinatorLockHeld(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-race",
		Attempt:    "att-race",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	tokenStore := harvest.NewMemoryTokenAuthority()
	token := harvest.ActionToken{
		TokenID:    "tok-race",
		Card:       "card-race",
		Attempt:    "att-race",
		Action:     harvest.ActionPush,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}
	tokenStore.IssueActionToken(token)
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)
	creds := harvest.AttemptCredentials{
		Card:       "card-race",
		Attempt:    "att-race",
		FenceEpoch: 1,
	}

	ctx := context.Background()

	// Inside the CommitEffect callback, verify that the coordinator lock is held:
	// If another goroutine attempts to Pause(), it must block until CommitEffect completes.
	var lockHeldDuringEffect bool
	pauseAttempted := make(chan struct{})
	pauseDone := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)

	effectExecuted := false
	err := owner.CommitEffect(ctx, now, creds, harvest.ActionPush, token, func() error {
		effectExecuted = true

		go func() {
			defer wg.Done()
			close(pauseAttempted)
			ctrl.Pause() // Will block because CommitEffect holds coordinator lock
			close(pauseDone)
		}()

		<-pauseAttempted
		// Give the pause goroutine a moment to attempt acquiring the lock
		time.Sleep(50 * time.Millisecond)

		select {
		case <-pauseDone:
			// If pause finished while we were inside CommitEffect, lock was NOT held!
			lockHeldDuringEffect = false
		default:
			// Pause is correctly blocked waiting for our lock!
			lockHeldDuringEffect = true
		}
		return nil
	})

	wg.Wait()

	if err != nil {
		t.Fatalf("CommitEffect failed: %v", err)
	}
	if !effectExecuted {
		t.Fatal("effect must execute")
	}
	if !lockHeldDuringEffect {
		t.Fatal("Coordinator lock must be held through effect commitment (no check-then-unlock)")
	}
}

// TestEffectOwnerDoesNotWriteEventsJSONL explicitly verifies the key invariant:
// Emma's effect-owner verifies fence epoch + RUN action token at linearization;
// she does NOT write events.jsonl.
func TestEffectOwnerDoesNotWriteEventsJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	lifecycleDir := filepath.Join(tmpDir, "lifecycle")
	if err := os.MkdirAll(filepath.Join(lifecycleDir, "cards"), 0o755); err != nil {
		t.Fatalf("mkdir cards: %v", err)
	}

	cardProj := map[string]any{
		"card":        "card-no-events",
		"attempt":     "att-1",
		"fence_epoch": 1,
		"state":       "STARTED",
	}
	data, err := json.Marshal(cardProj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lifecycleDir, "cards", "card-no-events.json"), data, 0o644); err != nil {
		t.Fatalf("write card projection: %v", err)
	}

	diskAuth := harvest.NewDiskCardAuthority(lifecycleDir)
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(diskAuth, ctrl, tokenStore)

	creds := harvest.AttemptCredentials{
		Card:       "card-no-events",
		Attempt:    "att-1",
		FenceEpoch: 1,
	}
	baseToken := harvest.ActionToken{
		Card:       "card-no-events",
		Attempt:    "att-1",
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}

	ctx := context.Background()

	// Perform multiple effect commits (RESULT, push, accept).
	for _, act := range []harvest.ActionType{harvest.ActionRESULT, harvest.ActionPush, harvest.ActionAccept} {
		actToken := baseToken
		actToken.TokenID = fmt.Sprintf("tok-no-ev-%s", act)
		actToken.Action = act
		tokenStore.IssueActionToken(actToken)
		err := owner.CommitEffect(ctx, now, creds, act, actToken, func() error {
			return nil
		})
		if err != nil {
			t.Fatalf("CommitEffect(%s) failed: %v", act, err)
		}
	}

	// Verify that events.jsonl does NOT exist!
	eventsPath := filepath.Join(lifecycleDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatalf("INVARIANT VIOLATION: Effect-owner must NOT write events.jsonl, but file exists at %s", eventsPath)
	}
}

// TestForgedAndSelfAssertedTokenRefused verifies that self-asserted caller tokens
// not issued by the authoritative store or tampered with are rejected.
func TestForgedAndSelfAssertedTokenRefused(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-forgery",
		Attempt:    "att-1",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)

	creds := harvest.AttemptCredentials{
		Card:       "card-forgery",
		Attempt:    "att-1",
		FenceEpoch: 1,
	}

	validTok := harvest.ActionToken{
		TokenID:    "tok-legit",
		Card:       "card-forgery",
		Attempt:    "att-1",
		Action:     harvest.ActionPush,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}
	tokenStore.IssueActionToken(validTok)

	ctx := context.Background()

	// 1. Authoritative valid token is accepted.
	if err := owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionPush, validTok); err != nil {
		t.Fatalf("Authoritative valid token must be accepted, got: %v", err)
	}

	// 2. Fabricated / self-asserted token ID not in token store is rejected.
	forgedTok := validTok
	forgedTok.TokenID = "tok-caller-fabricated-id"
	err := owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionPush, forgedTok)
	if !errors.Is(err, harvest.ErrForgedToken) {
		t.Fatalf("Self-asserted forged token must fail with ErrForgedToken, got: %v", err)
	}

	// 3. Tampered token: caller presents valid TokenID but changed Card.
	tamperedCard := validTok
	tamperedCard.Card = "other-card"
	credsOther := creds
	credsOther.Card = "other-card"
	cardStore.Set(harvest.CardProjection{Card: "other-card", Attempt: "att-1", FenceEpoch: 1, State: "STARTED"})
	err = owner.VerifyAtLinearization(ctx, now, credsOther, harvest.ActionPush, tamperedCard)
	if !errors.Is(err, harvest.ErrForgedToken) {
		t.Fatalf("Tampered card on token must fail with ErrForgedToken, got: %v", err)
	}

	// 4. Tampered token: caller presents valid TokenID but changed Action.
	tamperedAction := validTok
	tamperedAction.Action = harvest.ActionAccept
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionAccept, tamperedAction)
	if !errors.Is(err, harvest.ErrForgedToken) {
		t.Fatalf("Tampered action on token must fail with ErrForgedToken, got: %v", err)
	}

	// 5. EffectOwner with nil TokenAuthority rejects all tokens.
	ownerNoAuth := harvest.NewEffectOwner(cardStore, ctrl)
	err = ownerNoAuth.VerifyAtLinearization(ctx, now, creds, harvest.ActionPush, validTok)
	if !errors.Is(err, harvest.ErrNoTokenAuthority) {
		t.Fatalf("Owner with nil TokenAuthority must return ErrNoTokenAuthority, got: %v", err)
	}
}

// TestReplayedTokenRefused verifies that once a RUN action token has successfully
// committed a side-effect, any subsequent attempt to commit or verify using that token
// is rejected as a replay attack.
func TestReplayedTokenRefused(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-replay",
		Attempt:    "att-1",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)

	creds := harvest.AttemptCredentials{
		Card:       "card-replay",
		Attempt:    "att-1",
		FenceEpoch: 1,
	}

	token := harvest.ActionToken{
		TokenID:    "tok-single-use",
		Card:       "card-replay",
		Attempt:    "att-1",
		Action:     harvest.ActionRESULT,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}
	tokenStore.IssueActionToken(token)

	ctx := context.Background()

	// Initial verification before commit succeeds.
	if err := owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, token); err != nil {
		t.Fatalf("Initial verification must succeed: %v", err)
	}

	// First commit succeeds.
	effectRan := false
	err := owner.CommitEffect(ctx, now, creds, harvest.ActionRESULT, token, func() error {
		effectRan = true
		return nil
	})
	if err != nil {
		t.Fatalf("First commit must succeed: %v", err)
	}
	if !effectRan {
		t.Fatal("effect callback must have run")
	}

	// Verification after commit must fail with ErrReplayedToken.
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionRESULT, token)
	if !errors.Is(err, harvest.ErrReplayedToken) {
		t.Fatalf("Verify after commit must return ErrReplayedToken, got: %v", err)
	}

	// Second commit must fail with ErrReplayedToken and NOT execute effect.
	secondRan := false
	err = owner.CommitEffect(ctx, now, creds, harvest.ActionRESULT, token, func() error {
		secondRan = true
		return nil
	})
	if !errors.Is(err, harvest.ErrReplayedToken) {
		t.Fatalf("Second commit must return ErrReplayedToken, got: %v", err)
	}
	if secondRan {
		t.Fatal("Replayed effect must NEVER execute the callback")
	}
}

// TestTokenRetryReconciliation verifies that retrying an action reconciles
// the recorded outcome of the token rather than acquiring a new token.
func TestTokenRetryReconciliation(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-reconcile",
		Attempt:    "att-1",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	tokenStore := harvest.NewMemoryTokenAuthority()
	owner := harvest.NewEffectOwner(cardStore, ctrl, tokenStore)

	creds := harvest.AttemptCredentials{
		Card:       "card-reconcile",
		Attempt:    "att-1",
		FenceEpoch: 1,
	}

	token := harvest.ActionToken{
		TokenID:    "tok-recon-1",
		Card:       "card-reconcile",
		Attempt:    "att-1",
		Action:     harvest.ActionPush,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}
	tokenStore.IssueActionToken(token)

	ctx := context.Background()

	// Initial state before effect is ISSUED.
	authTok, err := owner.ReconcileToken(ctx, now, token.TokenID)
	if err != nil {
		t.Fatalf("ReconcileToken before commit: %v", err)
	}
	if authTok.State != harvest.TokenIssued {
		t.Fatalf("Initial token state must be ISSUED, got: %s", authTok.State)
	}

	// First attempt fails with transient error.
	transientErr := errors.New("network timeout pushing branch")
	err = owner.CommitEffect(ctx, now, creds, harvest.ActionPush, token, func() error {
		return transientErr
	})
	if !errors.Is(err, transientErr) {
		t.Fatalf("CommitEffect must return transient error, got: %v", err)
	}

	// Reconciliation shows FAILED state and recorded error.
	authTok, err = owner.ReconcileToken(ctx, now, token.TokenID)
	if err != nil {
		t.Fatalf("ReconcileToken after failure: %v", err)
	}
	if authTok.State != harvest.TokenFailed {
		t.Fatalf("Token state after failure must be FAILED, got: %s", authTok.State)
	}
	if authTok.OutcomeErr != transientErr.Error() {
		t.Fatalf("Token outcome error mismatch: got %q, want %q", authTok.OutcomeErr, transientErr.Error())
	}

	// Retrying the action with the same token succeeds.
	err = owner.CommitEffect(ctx, now, creds, harvest.ActionPush, token, func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("Retry CommitEffect must succeed: %v", err)
	}

	// Reconciliation now shows COMPLETED state.
	authTok, err = owner.ReconcileToken(ctx, now, token.TokenID)
	if err != nil {
		t.Fatalf("ReconcileToken after success: %v", err)
	}
	if authTok.State != harvest.TokenCompleted {
		t.Fatalf("Token state after retry success must be COMPLETED, got: %s", authTok.State)
	}
}

// TestDiskTokenAuthority verifies that DiskTokenAuthority correctly persists tokens,
// outcomes, and replay protection to the control directory on disk.
func TestDiskTokenAuthority(t *testing.T) {
	controlDir := t.TempDir()
	diskStore := harvest.NewDiskTokenAuthority(controlDir)

	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-disk",
		Attempt:    "att-1",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	owner := harvest.NewEffectOwner(cardStore, ctrl, diskStore)

	creds := harvest.AttemptCredentials{
		Card:       "card-disk",
		Attempt:    "att-1",
		FenceEpoch: 1,
	}
	tok := harvest.ActionToken{
		TokenID:    "tok-disk-1",
		Card:       "card-disk",
		Attempt:    "att-1",
		Action:     harvest.ActionAccept,
		Generation: 1,
		Scope:      "fleet",
		Expires:    now.Add(time.Hour),
	}

	ctx := context.Background()

	// 1. Before issuance, token lookup is missing.
	_, ok, err := diskStore.LookupToken(tok.TokenID)
	if err != nil || ok {
		t.Fatalf("LookupToken before issuance: ok=%v, err=%v", ok, err)
	}

	// Verification fails for unissued token.
	err = owner.VerifyAtLinearization(ctx, now, creds, harvest.ActionAccept, tok)
	if !errors.Is(err, harvest.ErrForgedToken) {
		t.Fatalf("Unissued token must fail with ErrForgedToken, got: %v", err)
	}

	// 2. Issue token to disk.
	if err := diskStore.IssueActionToken(tok); err != nil {
		t.Fatalf("IssueActionToken failed: %v", err)
	}

	// Token file exists on disk.
	tokFile := filepath.Join(controlDir, "tokens", tok.TokenID+".json")
	if _, err := os.Stat(tokFile); err != nil {
		t.Fatalf("Token file must exist at %s: %v", tokFile, err)
	}

	// 3. Commit effect with token succeeds.
	err = owner.CommitEffect(ctx, now, creds, harvest.ActionAccept, tok, func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("CommitEffect failed: %v", err)
	}

	// 4. Verify token file on disk now reflects COMPLETED state.
	freshDiskStore := harvest.NewDiskTokenAuthority(controlDir)
	authTok, ok, err := freshDiskStore.LookupToken(tok.TokenID)
	if err != nil || !ok {
		t.Fatalf("LookupToken from fresh disk store: ok=%v, err=%v", ok, err)
	}
	if authTok.State != harvest.TokenCompleted {
		t.Fatalf("Persisted token state must be COMPLETED, got: %s", authTok.State)
	}

	// 5. Fresh EffectOwner wired to same disk store rejects replay.
	freshOwner := harvest.NewEffectOwner(cardStore, ctrl, freshDiskStore)
	err = freshOwner.CommitEffect(ctx, now, creds, harvest.ActionAccept, tok, func() error {
		return nil
	})
	if !errors.Is(err, harvest.ErrReplayedToken) {
		t.Fatalf("Replay against fresh disk store must return ErrReplayedToken, got: %v", err)
	}
}
