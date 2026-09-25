package harvest_test

// SPEC-PULSE required tests 5 and 9 for durable launch publication fencing.
// The effect owner verifies the card fence epoch and a bounded RUN action
// token at her own linearization. She does not write events.jsonl.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/harvest"
)

func TestDurableLaunch5_TwoLiveWorkersCurrentFencePublishes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cards := harvest.NewMemory()
	cards.Set(harvest.Projection{
		Card: "card1", Attempt: "attempt-old", FenceEpoch: 1, Attempts: 1, State: "STARTED",
	})
	owner := harvest.New(cards, ctrl)
	ctx := context.Background()

	stale := harvest.Credential{Card: "card1", Attempt: "attempt-old", FenceEpoch: 1}
	current := harvest.Credential{Card: "card1", Attempt: "attempt-new", FenceEpoch: 2}

	cards.Set(harvest.Projection{
		Card: "card1", Attempt: "attempt-new", FenceEpoch: 2, Attempts: 2, State: "STARTED",
	})

	for _, action := range []harvest.Action{harvest.ActionRESULT, harvest.ActionPush, harvest.ActionAccept} {
		ran := false
		err := owner.Commit(ctx, now, stale, action, tokenFor(stale, action, 1, now.Add(time.Hour)), func() error {
			ran = true
			return nil
		})
		if err == nil {
			t.Fatalf("stale worker %s: committed, want fence refusal", action)
		}
		if !errors.Is(err, harvest.ErrStaleFence) && !errors.Is(err, harvest.ErrAttempt) {
			t.Fatalf("stale worker %s: %v, want ErrStaleFence or ErrAttempt", action, err)
		}
		if ran {
			t.Fatalf("stale worker %s ran the effect", action)
		}
	}

	proj, ok, err := cards.Lookup("card1")
	if err != nil || !ok {
		t.Fatalf("lookup after stale refusal: ok=%v err=%v", ok, err)
	}
	if proj.FenceEpoch != 2 || proj.Attempt != "attempt-new" || proj.Attempts != 2 {
		t.Fatalf("refusal consumed a third attempt or moved the fence: %+v", proj)
	}

	for _, action := range []harvest.Action{harvest.ActionRESULT, harvest.ActionPush, harvest.ActionAccept} {
		ran := false
		err := owner.Commit(ctx, now, current, action, tokenFor(current, action, 1, now.Add(time.Hour)), func() error {
			ran = true
			return nil
		})
		if err != nil {
			t.Fatalf("current worker %s: %v", action, err)
		}
		if !ran {
			t.Fatalf("current worker %s did not run the effect", action)
		}
	}

	proj, ok, err = cards.Lookup("card1")
	if err != nil || !ok {
		t.Fatalf("lookup after current publish: ok=%v err=%v", ok, err)
	}
	if proj.Attempts != 2 {
		t.Fatalf("publication consumed a third attempt: %+v", proj)
	}
}

func TestDurableLaunch5_SeparateRUNActionTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cards := harvest.NewMemory()
	cred := harvest.Credential{Card: "card-sep", Attempt: "att-sep", FenceEpoch: 1}
	cards.Set(harvest.Projection{Card: cred.Card, Attempt: cred.Attempt, FenceEpoch: 1, Attempts: 1, State: "STARTED"})
	owner := harvest.New(cards, ctrl)
	ctx := context.Background()

	resultTok := tokenFor(cred, harvest.ActionRESULT, 1, now.Add(time.Hour))
	if err := owner.Commit(ctx, now, cred, harvest.ActionPush, resultTok, func() error {
		t.Fatal("RESULT token must not push")
		return nil
	}); !errors.Is(err, harvest.ErrAction) {
		t.Fatalf("RESULT token reused for push: %v, want ErrAction", err)
	}
	if err := owner.Commit(ctx, now, cred, harvest.ActionAccept, resultTok, func() error {
		t.Fatal("RESULT token must not accept")
		return nil
	}); !errors.Is(err, harvest.ErrAction) {
		t.Fatalf("RESULT token reused for accept: %v, want ErrAction", err)
	}
	if err := owner.Commit(ctx, now, cred, harvest.ActionRESULT, harvest.Token{}, func() error {
		t.Fatal("missing token must not publish RESULT")
		return nil
	}); !errors.Is(err, harvest.ErrToken) {
		t.Fatalf("missing token: %v, want ErrToken", err)
	}
}

func TestDurableLaunch9_PauseCaptureCannotBecomeAuthoritativeResult(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cards := harvest.NewMemory()
	cred := harvest.Credential{Card: "card-p9", Attempt: "att-p9", FenceEpoch: 1}
	cards.Set(harvest.Projection{Card: cred.Card, Attempt: cred.Attempt, FenceEpoch: 1, Attempts: 1, State: "STARTED"})
	owner := harvest.New(cards, ctrl)
	ctx := context.Background()

	if err := owner.Commit(ctx, now, cred, harvest.ActionRESULT, tokenFor(cred, harvest.ActionRESULT, 1, now.Add(time.Hour)), nil); err != nil {
		t.Fatalf("RESULT under RUN: %v", err)
	}

	ctrl.Pause()

	dir := t.TempDir()
	body := []byte("attempt-local RESULT evidence\n")
	if err := harvest.Capture(dir, "RESULT.md", body); err != nil {
		t.Fatalf("private capture during PAUSE: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "RESULT.md"))
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("capture wrote %q, want %q", got, body)
	}

	for _, action := range []harvest.Action{harvest.ActionRESULT, harvest.ActionPush, harvest.ActionAccept} {
		ran := false
		err := owner.Commit(ctx, now, cred, action, tokenFor(cred, action, 1, now.Add(time.Hour)), func() error {
			ran = true
			return nil
		})
		if err == nil {
			t.Fatalf("%s during PAUSE committed", action)
		}
		if !errors.Is(err, harvest.ErrNotRun) && !errors.Is(err, harvest.ErrGeneration) {
			t.Fatalf("%s during PAUSE: %v, want ErrNotRun or ErrGeneration", action, err)
		}
		if ran {
			t.Fatalf("%s during PAUSE ran the effect", action)
		}
	}

	ctrl.Resume(now.Add(time.Hour))
	old := tokenFor(cred, harvest.ActionRESULT, 1, now.Add(time.Hour))
	if err := owner.Commit(ctx, now, cred, harvest.ActionRESULT, old, nil); !errors.Is(err, harvest.ErrGeneration) {
		t.Fatalf("token issued before PAUSE after RUN n+1: %v, want ErrGeneration", err)
	}
	if err := owner.Commit(ctx, now, cred, harvest.ActionRESULT, tokenFor(cred, harvest.ActionRESULT, ctrl.Generation(), now.Add(time.Hour)), nil); err != nil {
		t.Fatalf("fresh token at generation %d: %v", ctrl.Generation(), err)
	}
}

func TestEffectOwnerDoesNotWriteEventsJSONL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	life := filepath.Join(root, "lifecycle")
	if err := os.MkdirAll(filepath.Join(life, "cards"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"card": "card-ev", "attempt": "att-ev", "fence_epoch": 1, "attempts": 1, "state": "STARTED",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(life, "cards", "card-ev.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	cards, err := harvest.OpenCards(life)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	owner := harvest.New(cards, ctrl)
	cred := harvest.Credential{Card: "card-ev", Attempt: "att-ev", FenceEpoch: 1}
	ctx := context.Background()
	for _, action := range []harvest.Action{harvest.ActionRESULT, harvest.ActionPush, harvest.ActionAccept} {
		if err := owner.Commit(ctx, now, cred, action, tokenFor(cred, action, 1, now.Add(time.Hour)), nil); err != nil {
			t.Fatalf("Commit(%s): %v", action, err)
		}
	}
	if _, err := os.Stat(filepath.Join(life, "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("effect owner wrote events.jsonl: %v", err)
	}
}

func TestReview2153EmptyScopeMustRefuse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	owner, cred := reviewOwner(t, now, "card-scope", "att-scope", 1)
	tok := tokenFor(cred, harvest.ActionPush, 1, now.Add(time.Hour))
	tok.Scope = ""
	ran := false
	err := owner.Commit(context.Background(), now, cred, harvest.ActionPush, tok, func() error {
		ran = true
		return nil
	})
	if err == nil || ran {
		t.Fatalf("empty scope authorized effect: err=%v ran=%v", err, ran)
	}
	if !errors.Is(err, harvest.ErrScope) {
		t.Fatalf("empty scope: %v, want ErrScope", err)
	}
}

func TestReview2153TokenReplayMustNotRepeatEffect(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	owner, cred := reviewOwner(t, now, "card-replay", "att-replay", 1)
	tok := tokenFor(cred, harvest.ActionPush, 1, now.Add(time.Hour))
	n := 0
	effect := func() error { n++; return nil }
	if err := owner.Commit(context.Background(), now, cred, harvest.ActionPush, tok, effect); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := owner.Commit(context.Background(), now, cred, harvest.ActionPush, tok, effect); err != nil {
		t.Fatalf("identical retry must reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("same token ran effect %d times, want one plus reconciliation", n)
	}
	other := tok
	other.Generation = 2
	if err := owner.Commit(context.Background(), now, cred, harvest.ActionPush, other, effect); err == nil {
		t.Fatal("same token id with a different payload must refuse")
	}
	if n != 1 {
		t.Fatalf("different payload re-ran the effect: n=%d", n)
	}
}

func TestReview2153DiskCardIdentityMustMatchRequestedCard(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	life := filepath.Join(root, "lifecycle")
	if err := os.MkdirAll(filepath.Join(life, "cards"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"card": "other", "attempt": "att-wanted", "fence_epoch": 1, "attempts": 1, "state": "STARTED",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(life, "cards", "wanted.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	cards, err := harvest.OpenCards(life)
	if err != nil {
		t.Fatal(err)
	}
	owner := harvest.New(cards, harvest.NewFakeControl(1, now.Add(time.Hour)))
	cred := harvest.Credential{Card: "wanted", Attempt: "att-wanted", FenceEpoch: 1}
	tok := tokenFor(cred, harvest.ActionRESULT, 1, now.Add(time.Hour))
	ran := false
	err = owner.Commit(context.Background(), now, cred, harvest.ActionRESULT, tok, func() error {
		ran = true
		return nil
	})
	if err == nil || ran {
		t.Fatalf("projection for other card authorized wanted: err=%v ran=%v", err, ran)
	}
}

func reviewOwner(t *testing.T, now time.Time, card, attempt string, epoch int) (*harvest.Owner, harvest.Credential) {
	t.Helper()
	cards := harvest.NewMemory()
	cred := harvest.Credential{Card: card, Attempt: attempt, FenceEpoch: epoch}
	cards.Set(harvest.Projection{Card: card, Attempt: attempt, FenceEpoch: epoch, Attempts: 1, State: "STARTED"})
	return harvest.New(cards, harvest.NewFakeControl(1, now.Add(time.Hour))), cred
}

func tokenFor(cred harvest.Credential, action harvest.Action, gen int, exp time.Time) harvest.Token {
	return harvest.Token{
		ID:         "tok-" + string(action) + "-" + cred.Attempt + "-" + strconv.Itoa(gen),
		Card:       cred.Card,
		Attempt:    cred.Attempt,
		Action:     action,
		Generation: gen,
		Scope:      "fleet",
		Expires:    exp,
	}
}
