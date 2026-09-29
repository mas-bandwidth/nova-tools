//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func TestFriendCLI(t *testing.T) {
	t.Parallel()

	addr, client := benchRoleFixture(t)
	ctx := context.Background()

	// Seed friends
	f := "f1"
	client.SAdd(ctx, "friends", f)
	client.HSet(ctx, "friend:"+f+":desired", "slots", "4", "machine", "studio", "paused", "0")

	// 1. friend tell
	code, out, errOut := runVerb(t, "friend", "tell", "--redis", addr, "--from", "rowan", f, "wake up, tasks ready")
	if code != 0 || !strings.Contains(out, "TELL friend=f1 from=rowan") || !strings.Contains(out, "wake up, tasks ready") || errOut != "" {
		t.Fatalf("friend tell = %d %q %q", code, out, errOut)
	}

	// Verify ev:friend stream was written
	msgs := client.XRevRangeN(ctx, friend.StreamFriendEvents, "+", "-", 1).Val()
	if len(msgs) == 0 || msgs[0].Values["to"] != f || msgs[0].Values["text"] != "wake up, tasks ready" {
		t.Fatalf("ev:friend stream entry mismatch: %v", msgs)
	}

	// 2. friend tiers
	code, out, errOut = runVerb(t, "friend", "tiers", "--redis", addr, f, "flash,pro")
	if code != 0 || out != "FRIEND TIERS friend=f1 tiers=flash,pro\n" || errOut != "" {
		t.Fatalf("friend tiers = %d %q %q", code, out, errOut)
	}
	if got := client.HGet(ctx, "friend:"+f+":desired", "tiers").Val(); got != "flash,pro" {
		t.Fatalf("friend:f1:desired tiers = %q, want 'flash,pro'", got)
	}

	// 3. friend slots
	code, out, errOut = runVerb(t, "friend", "slots", "--redis", addr, f, "8")
	if code != 0 || out != "FRIEND SLOTS friend=f1 slots=8\n" || errOut != "" {
		t.Fatalf("friend slots = %d %q %q", code, out, errOut)
	}
	if got := client.HGet(ctx, "friend:"+f+":desired", "slots").Val(); got != "8" {
		t.Fatalf("friend:f1:desired slots = %q, want '8'", got)
	}

	// 4. friend pause
	code, out, errOut = runVerb(t, "friend", "pause", "--redis", addr, f)
	if code != 0 || out != "PAUSED friend:f1\n" || errOut != "" {
		t.Fatalf("friend pause = %d %q %q", code, out, errOut)
	}
	if got := client.HGet(ctx, "friend:"+f+":desired", "paused").Val(); got != "1" {
		t.Fatalf("friend:f1:desired paused = %q, want '1'", got)
	}

	// 5. friend resume
	code, out, errOut = runVerb(t, "friend", "resume", "--redis", addr, f)
	if code != 0 || out != "RESUMED friend:f1\n" || errOut != "" {
		t.Fatalf("friend resume = %d %q %q", code, out, errOut)
	}
	if got := client.HGet(ctx, "friend:"+f+":desired", "paused").Val(); got != "0" {
		t.Fatalf("friend:f1:desired paused = %q, want '0'", got)
	}

	// 6. friend ask
	cardID := "card-ask-cli-1"
	pushReq := taskcard.PushRequest{
		ID:     cardID,
		Where:  "ready",
		Stream: "s:ask-cli",
		Sprint: "sprint-ask",
		Kind:   "build",
		Title:  "implement ask verb WHO: any | MODEL: pro | PATHS: internal/friend/ | DONE-WHEN: tests pass",
		By:     "rowan",
	}
	if _, err := taskcard.Push(ctx, client, pushReq); err != nil {
		t.Fatalf("Push card: %v", err)
	}

	code, out, errOut = runVerb(t, "friend", "ask", "--redis", addr, "--card", cardID, "--as", "rowan", f)
	if code != 0 || !strings.Contains(out, "ASK friend=f1 card=card-ask-cli-1") || errOut != "" {
		t.Fatalf("friend ask = %d %q %q", code, out, errOut)
	}

	// Verify ask message in ev:friend
	askMsgs := client.XRevRangeN(ctx, friend.StreamFriendEvents, "+", "-", 1).Val()
	if len(askMsgs) == 0 || askMsgs[0].Values["card"] != cardID || askMsgs[0].Values["to"] != f {
		t.Fatalf("ev:friend stream entry for ask mismatch: %v", askMsgs)
	}
}
