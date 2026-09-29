//go:build functional

package sprint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestStellaSprintCheckedPayloadDrivesExecution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, base := live(t)
	jw, err := OpenJournalWriter(filepath.Join(t.TempDir(), "events.journal"))
	if err != nil {
		t.Fatal(err)
	}
	defer jw.Close()
	client := NewRedisCardClient(base.RDB(), WithJournalWriter(jw))
	ev := JournalMutationEvent{Action: ActPush, Epoch: 1, Actor: "fixture", CardID: "checked-card", Stream: "main"}
	tx, err := NewCardTransaction(1, ev, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	tx.Event.CardID = "executed-card"
	result, err := client.ExecuteTransaction(ctx, tx)
	if err != nil {
		t.Logf("inconsistent event refused: %v", err)
		return
	}
	var recorded JournalMutationEvent
	if result.Frame == nil {
		t.Fatal("accepted but no journal frame")
	}
	if err = json.Unmarshal(result.Frame.Payload, &recorded); err != nil {
		t.Fatal(err)
	}
	actual := base.RDB().Exists(ctx, MemberKey("executed-card")).Val()
	checked := base.RDB().Exists(ctx, MemberKey("checked-card")).Val()
	t.Logf("recorded_card=%s executed_card_exists=%d checked_card_exists=%d", recorded.CardID, actual, checked)
	if actual != 0 || checked != 1 {
		t.Errorf("CRC-checked journal event differs from executed mutation")
	}
}

func TestStellaSprintRollbackRestoresStreamAndShadow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, base := live(t)
	shadow := NewMemoryCardMachine(1)
	client := NewRedisCardClient(base.RDB(), WithShadowVerifier(shadow))
	ev := JournalMutationEvent{Action: ActPush, Epoch: 1, Actor: "fixture", CardID: "first", Stream: "main"}
	first, err := NewCardTransaction(1, ev, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ExecuteTransaction(ctx, first); err != nil {
		t.Fatal(err)
	}
	beforeLen, err := base.RDB().XLen(ctx, "table:streams:changes").Result()
	if err != nil {
		t.Fatal(err)
	}
	beforeHash := shadow.StateHash()
	ev.CardID = "refused-card"
	bad, err := NewCardTransaction(2, ev, sha256.Sum256([]byte("wrong-hash")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExecuteTransaction(ctx, bad)
	if !errors.Is(err, ErrStateHashMismatch) {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	afterLen, e := base.RDB().XLen(ctx, "table:streams:changes").Result()
	if e != nil {
		t.Fatal(e)
	}
	afterHash := shadow.StateHash()
	seq, e := client.Ratchet().Get(ctx)
	if e != nil {
		t.Fatal(e)
	}
	exists, e := base.RDB().Exists(ctx, MemberKey("refused-card")).Result()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("refusal=%v stream_length=%d->%d shadow_equal=%v ratchet=%d refused_member_exists=%d", err, beforeLen, afterLen, beforeHash == afterHash, seq, exists)
	if beforeLen != afterLen {
		t.Error("failed transaction left a change-stream event")
	}
	if beforeHash != afterHash {
		t.Error("failed transaction mutated shadow state")
	}
	retry, e := NewCardTransaction(2, ev, [32]byte{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.ExecuteTransaction(ctx, retry)
	t.Logf("valid retry error=%v", e)
	if e != nil {
		t.Errorf("valid retry after rollback refused: %v", e)
	}
}

func TestStellaSprintEpochZeroRollbackRestoresCell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, base := live(t)
	client := NewRedisCardClient(base.RDB(), WithShadowVerifier(NewMemoryCardMachine(0)))
	ev := JournalMutationEvent{Action: ActPush, Epoch: 0, Actor: "fixture", CardID: "refused-zero", Stream: "main"}
	tx, e := NewCardTransaction(1, ev, sha256.Sum256([]byte("wrong-hash")))
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.ExecuteTransaction(ctx, tx)
	if !errors.Is(e, ErrStateHashMismatch) {
		t.Fatalf("expected hash mismatch, got %v", e)
	}
	members, e := base.RDB().ZRange(ctx, "table:streams:cell:main:waiting", 0, -1).Result()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("cell members after refusal=%v", members)
	if len(members) != 0 {
		t.Error("refused epoch-zero push left a cell member")
	}
}

func TestStellaSprintRatchetKeepsExactUint64(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, rdb := throwaway(t)
	for _, target := range []uint64{9007199254740993, 18446744073709551615} {
		r := NewSequenceRatchet(rdb, "ratchet:probe")
		if e := r.Set(ctx, 0); e != nil {
			t.Fatal(e)
		}
		reported, e := r.Ratchet(ctx, target)
		if e != nil {
			t.Errorf("target %d refused: %v", target, e)
			continue
		}
		actual, e := r.Get(ctx)
		raw, re := rdb.Get(ctx, r.Key()).Result()
		if re != nil {
			t.Fatal(re)
		}
		t.Logf("target=%d reported=%d actual=%d raw=%q read_error=%v", target, reported, actual, raw, e)
		if e != nil || actual != target {
			t.Errorf("successful ratchet did not store exact uint64 target %d", target)
		}
	}
}
