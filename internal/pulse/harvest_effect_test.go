package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/harvest"
)

func TestHarvestWithEffectOwner_CurrentWorkerSucceeds(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/101\n")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
		Stdout: "PULSE OK id=p-effect-2 n=5 free-before=5 queued=1 batches=1 deadline=300",
	}})

	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-101",
		Attempt:    "att-101",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	owner := harvest.NewEffectOwner(cardStore, ctrl)

	contract := "RESULT card-101 owner/repo 101 rowan/br-101"
	body := contract + "\nVERDICT ok\nBRANCH rowan/br-101\nREPO owner/repo\n"
	addCard(t, root, "card-101", "0", "", contract, body)

	creds := map[string]harvest.AttemptCredentials{
		"card-101": {
			Card:       "card-101",
			Attempt:    "att-101",
			FenceEpoch: 1,
		},
	}
	pushTokens := map[string]harvest.ActionToken{
		"card-101": {
			TokenID:    "tok-push-101",
			Card:       "card-101",
			Attempt:    "att-101",
			Action:     harvest.ActionPush,
			Generation: 1,
			Scope:      "fleet",
			Expires:    now.Add(time.Hour),
		},
	}
	acceptTokens := map[string]harvest.ActionToken{
		"card-101": {
			TokenID:    "tok-acc-101",
			Card:       "card-101",
			Attempt:    "att-101",
			Action:     harvest.ActionAccept,
			Generation: 1,
			Scope:      "fleet",
			Expires:    now.Add(time.Hour),
		},
	}

	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID:           "p-effect-1",
		Root:         root,
		Sources:      filepath.Join(root, "sources.tsv"),
		Templates:    root,
		MaxBodyBytes: 4096,
		Max:          20,
		Stdout:       &out,
		Stderr:       &errs,
		Now:          func() time.Time { return now },
		EffectOwner:  owner,
		EffectCreds:  creds,
		PushTokens:   pushTokens,
		AcceptTokens: acceptTokens,
	})

	if code != 0 {
		t.Fatalf("Harvest code=%d, errs:\n%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "HARVEST PR repo=owner/repo pr=101 label=card-101 branch=rowan/br-101") {
		t.Fatalf("expected PR 101 in output, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "done=1 pushed=1 prs=1") {
		t.Fatalf("expected done=1 pushed=1 prs=1, got:\n%s", out.String())
	}

	// Invariant: events.jsonl must NOT be written by effect-owner.
	eventsFile := filepath.Join(root, "lifecycle", "events.jsonl")
	if _, err := os.Stat(eventsFile); !os.IsNotExist(err) {
		t.Fatalf("Invariant violated: effect-owner must not write %s", eventsFile)
	}
}

func TestHarvestWithEffectOwner_StaleFenceRefused(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/102\n")

	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	cardStore := harvest.NewMemoryCardAuthority()

	// Card was fenced to epoch 2 on attempt-2!
	cardStore.Set(harvest.CardProjection{
		Card:       "card-102",
		Attempt:    "att-102-new",
		FenceEpoch: 2,
		State:      "STARTED",
	})
	owner := harvest.NewEffectOwner(cardStore, ctrl)

	contract := "RESULT card-102 owner/repo 102 rowan/br-102"
	body := contract + "\nVERDICT ok\nBRANCH rowan/br-102\nREPO owner/repo\n"
	addCard(t, root, "card-102", "0", "", contract, body)

	// Worker presents stale credentials (attempt-102-old, epoch 1)
	creds := map[string]harvest.AttemptCredentials{
		"card-102": {
			Card:       "card-102",
			Attempt:    "att-102-old",
			FenceEpoch: 1,
		},
	}
	pushTokens := map[string]harvest.ActionToken{
		"card-102": {
			TokenID:    "tok-push-102",
			Card:       "card-102",
			Attempt:    "att-102-old",
			Action:     harvest.ActionPush,
			Generation: 1,
			Scope:      "fleet",
			Expires:    now.Add(time.Hour),
		},
	}
	acceptTokens := map[string]harvest.ActionToken{
		"card-102": {
			TokenID:    "tok-acc-102",
			Card:       "card-102",
			Attempt:    "att-102-old",
			Action:     harvest.ActionAccept,
			Generation: 1,
			Scope:      "fleet",
			Expires:    now.Add(time.Hour),
		},
	}

	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID:           "p-effect-stale",
		Root:         root,
		Sources:      filepath.Join(root, "sources.tsv"),
		Templates:    root,
		MaxBodyBytes: 4096,
		Max:          20,
		Stdout:       &out,
		Stderr:       &errs,
		Now:          func() time.Time { return now },
		EffectOwner:  owner,
		EffectCreds:  creds,
		PushTokens:   pushTokens,
		AcceptTokens: acceptTokens,
	})

	_ = code
	if !strings.Contains(errs.String(), "HARVEST EFFECT REFUSED label=card-102") {
		t.Fatalf("expected refusal in stderr, got:\n%s", errs.String())
	}
	if !strings.Contains(out.String(), "pushed=0 prs=0") {
		t.Fatalf("stale worker must not push or open PR, got:\n%s", out.String())
	}

	// Invariant: events.jsonl must NOT be written by effect-owner.
	eventsFile := filepath.Join(root, "lifecycle", "events.jsonl")
	if _, err := os.Stat(eventsFile); !os.IsNotExist(err) {
		t.Fatalf("Invariant violated: effect-owner must not write %s", eventsFile)
	}
}

func TestHarvestWithEffectOwner_ControlPausedRefused(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.com/owner/repo/pull/103\n")

	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	ctrl := harvest.NewFakeControl(1, now.Add(time.Hour))
	// Pause fleet control
	ctrl.Pause()

	cardStore := harvest.NewMemoryCardAuthority()
	cardStore.Set(harvest.CardProjection{
		Card:       "card-103",
		Attempt:    "att-103",
		FenceEpoch: 1,
		State:      "STARTED",
	})
	owner := harvest.NewEffectOwner(cardStore, ctrl)

	contract := "RESULT card-103 owner/repo 103 rowan/br-103"
	body := contract + "\nVERDICT ok\nBRANCH rowan/br-103\nREPO owner/repo\n"
	addCard(t, root, "card-103", "0", "", contract, body)

	creds := map[string]harvest.AttemptCredentials{
		"card-103": {
			Card:       "card-103",
			Attempt:    "att-103",
			FenceEpoch: 1,
		},
	}
	pushTokens := map[string]harvest.ActionToken{
		"card-103": {
			TokenID:    "tok-push-103",
			Card:       "card-103",
			Attempt:    "att-103",
			Action:     harvest.ActionPush,
			Generation: 1,
			Scope:      "fleet",
			Expires:    now.Add(time.Hour),
		},
	}

	var out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID:           "p-effect-pause",
		Root:         root,
		Sources:      filepath.Join(root, "sources.tsv"),
		Templates:    root,
		MaxBodyBytes: 4096,
		Max:          20,
		Stdout:       &out,
		Stderr:       &errs,
		Now:          func() time.Time { return now },
		EffectOwner:  owner,
		EffectCreds:  creds,
		PushTokens:   pushTokens,
	})

	_ = code
	if !strings.Contains(errs.String(), "HARVEST EFFECT REFUSED label=card-103") {
		t.Fatalf("expected refusal during PAUSE in stderr, got:\n%s", errs.String())
	}
	if !strings.Contains(out.String(), "pushed=0 prs=0") {
		t.Fatalf("PAUSE must not push or open PR, got:\n%s", out.String())
	}

	// Invariant: events.jsonl must NOT be written by effect-owner.
	eventsFile := filepath.Join(root, "lifecycle", "events.jsonl")
	if _, err := os.Stat(eventsFile); !os.IsNotExist(err) {
		t.Fatalf("Invariant violated: effect-owner must not write %s", eventsFile)
	}
}
