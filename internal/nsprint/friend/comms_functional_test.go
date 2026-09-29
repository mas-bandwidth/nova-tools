//go:build functional

package friend_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func TestFriendTellFunctional(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	res, err := friend.Tell(ctx, client, friend.TellRequest{
		Friend: "peer-f1",
		Text:   "hello peer 1, please review card c42",
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("Tell failed: %v", err)
	}

	if res.Friend != "peer-f1" || res.Actor != "rowan" || res.Text != "hello peer 1, please review card c42" {
		t.Fatalf("unexpected TellResult: %+v", res)
	}
	if res.EventID == "" {
		t.Fatal("expected non-empty EventID")
	}

	// Verify ev:friend stream entry
	msgs, err := client.XRange(ctx, friend.StreamFriendEvents, res.EventID, res.EventID).Result()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ev:friend stream entry lookup failed: %v, len=%d", err, len(msgs))
	}
	vals := msgs[0].Values
	if vals["to"] != "peer-f1" || vals["from"] != "rowan" || vals["text"] != "hello peer 1, please review card c42" || vals["kind"] != "tell" {
		t.Fatalf("unexpected values in ev:friend: %v", vals)
	}

	// Verify friend:outbox entry
	outboxMsgs, err := client.XRevRangeN(ctx, friend.OutboxKey, "+", "-", 1).Result()
	if err != nil || len(outboxMsgs) != 1 {
		t.Fatalf("friend:outbox lookup failed: %v, len=%d", err, len(outboxMsgs))
	}
	ovals := outboxMsgs[0].Values
	if ovals["friend"] != "peer-f1" || ovals["kind"] != "tell" || ovals["detail"] != "hello peer 1, please review card c42" {
		t.Fatalf("unexpected values in friend:outbox: %v", ovals)
	}
}

func TestFriendTiersFunctional(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	// 1. Set tiers
	res, err := friend.SetTiers(ctx, client, friend.TiersRequest{
		Friend: "peer-tiers",
		Tiers:  "flash,pro",
		Actor:  "emma",
	})
	if err != nil {
		t.Fatalf("SetTiers failed: %v", err)
	}
	if res.Tiers != "flash,pro" {
		t.Fatalf("expected tiers 'flash,pro', got %q", res.Tiers)
	}

	desired := client.HGetAll(ctx, "friend:peer-tiers:desired").Val()
	if desired["tiers"] != "flash,pro" {
		t.Fatalf("stored desired tiers = %q, want 'flash,pro'", desired["tiers"])
	}
	if desired["at"] == "" {
		t.Fatal("expected 'at' timestamp in desired hash")
	}
	if !client.SIsMember(ctx, "friends", "peer-tiers").Val() {
		t.Fatal("peer-tiers not in 'friends' set")
	}

	// 2. Clear tiers with "-"
	res2, err := friend.SetTiers(ctx, client, friend.TiersRequest{
		Friend: "peer-tiers",
		Tiers:  "-",
		Actor:  "emma",
	})
	if err != nil {
		t.Fatalf("SetTiers clear failed: %v", err)
	}
	if res2.Tiers != "" {
		t.Fatalf("expected cleared tiers, got %q", res2.Tiers)
	}
	desired2 := client.HGetAll(ctx, "friend:peer-tiers:desired").Val()
	if _, ok := desired2["tiers"]; ok {
		t.Fatalf("tiers field should have been deleted, got %q", desired2["tiers"])
	}

	// 3. Invalid tier refuses
	if _, err := friend.SetTiers(ctx, client, friend.TiersRequest{
		Friend: "peer-tiers",
		Tiers:  "invalid-model",
	}); err == nil {
		t.Fatal("expected error on invalid tier")
	}
}

func TestFriendSlotsFunctional(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	// 1. Set valid slots
	res, err := friend.SetSlots(ctx, client, friend.SlotsRequest{
		Friend: "peer-slots",
		Slots:  16,
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("SetSlots failed: %v", err)
	}
	if res.Slots != 16 {
		t.Fatalf("expected slots 16, got %d", res.Slots)
	}

	desired := client.HGetAll(ctx, "friend:peer-slots:desired").Val()
	if desired["slots"] != "16" {
		t.Fatalf("stored desired slots = %q, want '16'", desired["slots"])
	}
	if !client.SIsMember(ctx, "friends", "peer-slots").Val() {
		t.Fatal("peer-slots not in 'friends' set")
	}

	// 2. Negative slots refused
	if _, err := friend.SetSlots(ctx, client, friend.SlotsRequest{
		Friend: "peer-slots",
		Slots:  -1,
	}); err == nil {
		t.Fatal("expected error on negative slots")
	}
}

func TestFriendPauseResumeFunctional(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	// Seed friend
	client.SAdd(ctx, "friends", "peer-pause")
	client.HSet(ctx, "friend:peer-pause:desired", "slots", "4", "paused", "0")

	// 1. Pause
	res, err := friend.Pause(ctx, client, "peer-pause", "rowan")
	if err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	if !res.Paused || !res.Changed {
		t.Fatalf("expected Paused=true Changed=true, got %+v", res)
	}
	if got := client.HGet(ctx, "friend:peer-pause:desired", "paused").Val(); got != "1" {
		t.Fatalf("paused flag = %q, want '1'", got)
	}

	// 2. Pause again (no change)
	res2, err := friend.Pause(ctx, client, "peer-pause", "rowan")
	if err != nil {
		t.Fatalf("Pause again failed: %v", err)
	}
	if !res2.Paused || res2.Changed {
		t.Fatalf("expected Paused=true Changed=false, got %+v", res2)
	}

	// 3. Resume
	res3, err := friend.Resume(ctx, client, "peer-pause", "rowan")
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if res3.Paused || !res3.Changed {
		t.Fatalf("expected Paused=false Changed=true, got %+v", res3)
	}
	if got := client.HGet(ctx, "friend:peer-pause:desired", "paused").Val(); got != "0" {
		t.Fatalf("paused flag = %q, want '0'", got)
	}

	// 4. Unknown friend refuses
	if _, err := friend.Pause(ctx, client, "unknown-peer", "rowan"); err == nil {
		t.Fatal("expected error on unknown peer")
	}
}

func TestFriendAskFunctional(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	// Seed friend
	f := "peer-ask"
	client.SAdd(ctx, "friends", f)
	client.HSet(ctx, "friend:"+f+":desired", "slots", "4", "paused", "0")

	// Push a card
	cardID := "card-test-ask-1"
	stream := "ws:test-ask"
	sprint := "sprint-test"
	pushReq := taskcard.PushRequest{
		ID:     cardID,
		Where:  "ready",
		Stream: stream,
		Sprint: sprint,
		Kind:   "build",
		Title:  "implement ask verb WHO: any | MODEL: pro | PATHS: internal/friend/ | DONE-WHEN: tests pass",
		By:     "rowan",
	}
	if _, err := taskcard.Push(ctx, client, pushReq); err != nil {
		t.Fatalf("Push card: %v", err)
	}

	// Deal and ask via friend.Ask
	askRes, err := friend.Ask(ctx, client, friend.AskRequest{
		Friend: f,
		CardID: cardID,
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	if askRes.Friend != f || askRes.CardID != cardID || askRes.Actor != "rowan" {
		t.Fatalf("unexpected AskResult: %+v", askRes)
	}
	if askRes.EventID == "" {
		t.Fatal("expected non-empty EventID")
	}
	if askRes.Brief == "" {
		t.Fatal("expected non-empty Brief")
	}

	// Verify ev:friend stream entry
	msgs, err := client.XRange(ctx, friend.StreamFriendEvents, askRes.EventID, askRes.EventID).Result()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ev:friend stream entry lookup: %v, len=%d", err, len(msgs))
	}
	vals := msgs[0].Values
	if vals["to"] != f || vals["from"] != "rowan" || vals["kind"] != "ask" || vals["card"] != cardID {
		t.Fatalf("unexpected values in ev:friend for ask: %v", vals)
	}
	if vals["brief"] != askRes.Brief {
		t.Fatalf("ev:friend brief mismatch: got %q, want %q", vals["brief"], askRes.Brief)
	}

	// Verify friend:outbox entry
	outboxMsgs, err := client.XRevRangeN(ctx, friend.OutboxKey, "+", "-", 1).Result()
	if err != nil || len(outboxMsgs) != 1 {
		t.Fatalf("friend:outbox lookup: %v, len=%d", err, len(outboxMsgs))
	}
	ovals := outboxMsgs[0].Values
	if ovals["friend"] != f || ovals["kind"] != "ask" || ovals["card"] != cardID {
		t.Fatalf("unexpected values in friend:outbox for ask: %v", ovals)
	}
}
