//go:build functional

package sprint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// throwaway starts a disposable, isolated redis-server on a loopback port.
func throwaway(t *testing.T, extra ...string) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t, extra...)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	return addr, c
}

// live starts a disposable redis-server with the ns_card Function library loaded.
func live(t *testing.T, extra ...string) (string, *RedisCardClient) {
	t.Helper()
	addr, c := throwaway(t, extra...)
	ctx := context.Background()
	if err := LoadFunctionsStrict(ctx, c); err != nil {
		t.Fatalf("load ns_card library: %v", err)
	}
	cardClient := NewRedisCardClient(c)
	return addr, cardClient
}

// ============================================================================
// 1. Library Loading, Packaging, & Metadata Tests
// ============================================================================

func TestRedis_FunctionLibraryLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := throwaway(t)

	// 1. Initial state: library should not be loaded in throwaway store
	code, isLoaded, err := Loaded(ctx, c)
	if err != nil {
		t.Fatalf("Loaded check failed: %v", err)
	}
	if isLoaded {
		t.Fatalf("library unexpectedly loaded in clean throwaway store")
	}

	// 2. LoadMissing: installs library
	if err := LoadMissing(ctx, c); err != nil {
		t.Fatalf("LoadMissing failed: %v", err)
	}

	// Verify loaded
	code, isLoaded, err = Loaded(ctx, c)
	if err != nil {
		t.Fatalf("Loaded check failed: %v", err)
	}
	if !isLoaded || code == "" {
		t.Fatalf("library not loaded after LoadMissing")
	}

	// Verify Sum matches
	expectedSum := Sum(LibraryPayload)
	actualSum := Sum(code)
	if expectedSum != actualSum {
		t.Errorf("Sum mismatch: expected %s, got %s", expectedSum, actualSum)
	}

	// 3. LoadMissing idempotent check (should be a no-op)
	if err := LoadMissing(ctx, c); err != nil {
		t.Fatalf("second LoadMissing failed: %v", err)
	}

	// 4. LoadFunctionsStrict with replace
	if err := LoadFunctionsStrict(ctx, c); err != nil {
		t.Fatalf("LoadFunctionsStrict failed: %v", err)
	}
}

func TestRedis_FunctionLibraryPackaging(t *testing.T) {
	t.Parallel()

	payload, err := BuildLibraryPayload()
	if err != nil {
		t.Fatalf("BuildLibraryPayload failed: %v", err)
	}

	if !strings.HasPrefix(payload, "#!lua name=ns_card") {
		t.Errorf("library payload missing #!lua name=ns_card header")
	}

	if !strings.Contains(payload, "redis.register_function('ns_card_push'") {
		t.Errorf("library payload missing ns_card_push registration")
	}
	if !strings.Contains(payload, "redis.register_function('ns_card_deal_work'") {
		t.Errorf("library payload missing ns_card_deal_work registration")
	}
	if !strings.Contains(payload, "redis.register_function('ns_card_land'") {
		t.Errorf("library payload missing ns_card_land registration")
	}

	// Verify helper functions are present
	if !strings.Contains(payload, "local function cm_now()") {
		t.Errorf("library payload missing cm_now helper")
	}
	if !strings.Contains(payload, "local function cell_key(") {
		t.Errorf("library payload missing cell_key helper")
	}
	if !strings.Contains(payload, "local function refuse(") {
		t.Errorf("library payload missing refuse helper")
	}

	sum := Sum(payload)
	if len(sum) != 16 {
		t.Errorf("Sum length %d != 16", len(sum))
	}
}

// ============================================================================
// 2. Action 1: Push Tests
// ============================================================================

func TestRedis_Push_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	card := CardID("card-100")
	stream := "main"
	opts := WriteOptions{Epoch: 1, Actor: "test-runner"}

	receipt, err := client.Push(ctx, card, stream, opts)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	if receipt == nil {
		t.Fatal("receipt is nil")
	}
	if receipt.Outcome != "OK" {
		t.Errorf("receipt.Outcome = %s, want OK", receipt.Outcome)
	}
	if receipt.Epoch != 1 {
		t.Errorf("receipt.Epoch = %d, want 1", receipt.Epoch)
	}

	// Verify Redis Member Hash
	rdb := client.RDB()
	memberKey := MemberKey(string(card))
	where, err := rdb.HGet(ctx, memberKey, "where").Result()
	if err != nil {
		t.Fatalf("HGet where failed: %v", err)
	}
	if where != "waiting" {
		t.Errorf("where = %q, want waiting", where)
	}

	place, err := rdb.HGet(ctx, memberKey, "place:streams").Result()
	if err != nil {
		t.Fatalf("HGet place:streams failed: %v", err)
	}
	if place != "main:waiting" {
		t.Errorf("place:streams = %q, want main:waiting", place)
	}

	// Verify Streams cell ZSET
	score, err := rdb.ZScore(ctx, "table:streams:1:cell:main:waiting", string(card)).Result()
	if err != nil {
		t.Fatalf("ZScore failed: %v", err)
	}
	if score <= 0 {
		t.Errorf("ZScore = %f, want > 0", score)
	}

	// Verify revision counter
	rev, err := rdb.HGet(ctx, "table:streams:revision", "n").Result()
	if err != nil {
		t.Fatalf("HGet revision failed: %v", err)
	}
	if rev != "1" {
		t.Errorf("revision = %q, want 1", rev)
	}
}

func TestRedis_Push_DuplicateAndRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	card := CardID("card-dup")
	opts := WriteOptions{Epoch: 1, Actor: "test-actor"}

	// First push succeeds
	if _, err := client.Push(ctx, card, "main", opts); err != nil {
		t.Fatalf("first push failed: %v", err)
	}

	// Duplicate push must be refused
	_, err := client.Push(ctx, card, "main", opts)
	if err == nil {
		t.Fatal("expected error on duplicate push, got nil")
	}
	if !errors.Is(err, ErrDuplicateMember) {
		t.Errorf("expected ErrDuplicateMember, got %v", err)
	}

	// Push without actor must be refused
	cardNoActor := CardID("card-no-actor")
	_, err = client.Push(ctx, cardNoActor, "main", WriteOptions{Epoch: 1})
	if err == nil {
		t.Fatal("expected error on push without actor, got nil")
	}

	// Push with stale epoch
	rdb := client.RDB()
	_ = rdb.HSet(ctx, "table:streams:identity", "epoch", "2").Err()
	cardStale := CardID("card-stale")
	_, err = client.Push(ctx, cardStale, "main", WriteOptions{Epoch: 1, Actor: "actor"})
	if err == nil {
		t.Fatal("expected error on stale epoch, got nil")
	}
	if !errors.Is(err, ErrStaleEpoch) {
		t.Errorf("expected ErrStaleEpoch, got %v", err)
	}
}

func TestRedis_Push_EvalFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := throwaway(t)
	cardClient := NewRedisCardClient(c, WithForceEval(true))

	card := CardID("card-eval-push")
	opts := WriteOptions{Epoch: 1, Actor: "eval-runner"}

	receipt, err := cardClient.Push(ctx, card, "main", opts)
	if err != nil {
		t.Fatalf("Push via EVAL fallback failed: %v", err)
	}
	if receipt == nil || receipt.Outcome != "OK" {
		t.Errorf("receipt = %+v, want OK", receipt)
	}

	// Verify state in Redis
	where, err := c.HGet(ctx, MemberKey(string(card)), "where").Result()
	if err != nil {
		t.Fatalf("HGet failed: %v", err)
	}
	if where != "waiting" {
		t.Errorf("where = %q, want waiting", where)
	}
}

// ============================================================================
// 3. Action 3: DealWork Tests
// ============================================================================

func TestRedis_DealWork_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	card := CardID("card-deal-1")
	consumer := ConsumerID("bench:worker-1")
	opts := WriteOptions{Epoch: 1, Actor: "lead"}

	// 1. Set capacity
	if err := client.SetConsumerCapacity(ctx, consumer, 4); err != nil {
		t.Fatalf("SetConsumerCapacity failed: %v", err)
	}
	capVal, err := client.GetConsumerCapacity(ctx, consumer)
	if err != nil || capVal != 4 {
		t.Fatalf("GetConsumerCapacity = %d, want 4 (err=%v)", capVal, err)
	}

	// 2. Push card to waiting
	if _, err := client.Push(ctx, card, "main", opts); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// 3. Deal work
	res, err := client.DealWork(ctx, DealWorkParams{
		Card:     card,
		Consumer: consumer,
		Score:    10.0,
	}, opts)
	if err != nil {
		t.Fatalf("DealWork failed: %v", err)
	}

	if res.Receipt.Outcome != "OK" {
		t.Errorf("Outcome = %s, want OK", res.Receipt.Outcome)
	}
	expectedCopy := CopyID{Card: card, Attempt: 1}
	if res.Copy != expectedCopy {
		t.Errorf("Copy = %s, want %s", res.Copy, expectedCopy)
	}

	rdb := client.RDB()
	// Verify primary card in streams is now "working"
	primaryWhere, err := rdb.HGet(ctx, MemberKey(string(card)), "where").Result()
	if err != nil {
		t.Fatalf("HGet primary where failed: %v", err)
	}
	if primaryWhere != "working" {
		t.Errorf("primaryWhere = %q, want working", primaryWhere)
	}

	// Verify copy in fleet is in consumer's "working"
	copyKey := MemberKey(res.Copy.String())
	copyWhere, err := rdb.HGet(ctx, copyKey, "where").Result()
	if err != nil {
		t.Fatalf("HGet copy where failed: %v", err)
	}
	if copyWhere != "working" {
		t.Errorf("copyWhere = %q, want working", copyWhere)
	}

	copyPlace, err := rdb.HGet(ctx, copyKey, "place:fleet").Result()
	if err != nil {
		t.Fatalf("HGet copy place:fleet failed: %v", err)
	}
	if copyPlace != string(consumer)+":working" {
		t.Errorf("copyPlace = %q, want %s:working", copyPlace, consumer)
	}
}

func TestRedis_DealWork_Invariants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	consumer := ConsumerID("bench:worker-inv")
	opts := WriteOptions{Epoch: 1, Actor: "lead"}

	// Set capacity to 1
	if err := client.SetConsumerCapacity(ctx, consumer, 1); err != nil {
		t.Fatalf("SetConsumerCapacity failed: %v", err)
	}

	c1 := CardID("card-inv-1")
	c2 := CardID("card-inv-2")
	_, _ = client.Push(ctx, c1, "main", opts)
	_, _ = client.Push(ctx, c2, "main", opts)

	// First deal fills the 1 slot
	_, err := client.DealWork(ctx, DealWorkParams{Card: c1, Consumer: consumer}, opts)
	if err != nil {
		t.Fatalf("first deal failed: %v", err)
	}

	// Second deal must fail with ErrNoRoom
	_, err = client.DealWork(ctx, DealWorkParams{Card: c2, Consumer: consumer}, opts)
	if err == nil {
		t.Fatal("expected ErrNoRoom, got nil")
	}
	if !errors.Is(err, ErrNoRoom) {
		t.Errorf("expected ErrNoRoom, got %v", err)
	}

	// WorkingHasCopy: attempt to deal c1 again (already in working)
	_, err = client.DealWork(ctx, DealWorkParams{Card: c1, Consumer: "other-consumer"}, opts)
	if err == nil {
		t.Fatal("expected ErrWorkingHasCopy, got nil")
	}
	if !errors.Is(err, ErrWorkingHasCopy) {
		t.Errorf("expected ErrWorkingHasCopy, got %v", err)
	}

	// DepsNotMet: card with deps_unmet
	cDeps := CardID("card-deps")
	_, _ = client.Push(ctx, cDeps, "main", opts)
	_ = client.RDB().HSet(ctx, MemberKey(string(cDeps)), "deps_unmet", "1").Err()
	_, err = client.DealWork(ctx, DealWorkParams{Card: cDeps, Consumer: "other-consumer"}, opts)
	if err == nil {
		t.Fatal("expected ErrDepsNotMet, got nil")
	}
	if !errors.Is(err, ErrDepsNotMet) {
		t.Errorf("expected ErrDepsNotMet, got %v", err)
	}
}

func TestRedis_DealWork_EvalFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := throwaway(t)
	cardClient := NewRedisCardClient(c, WithForceEval(true))

	card := CardID("card-eval-deal")
	consumer := ConsumerID("bench:eval-worker")
	opts := WriteOptions{Epoch: 1, Actor: "eval-runner"}

	_ = cardClient.SetConsumerCapacity(ctx, consumer, 2)
	_, _ = cardClient.Push(ctx, card, "main", opts)

	res, err := cardClient.DealWork(ctx, DealWorkParams{Card: card, Consumer: consumer}, opts)
	if err != nil {
		t.Fatalf("DealWork via EVAL fallback failed: %v", err)
	}
	if res.Copy.Card != card || res.Copy.Attempt != 1 {
		t.Errorf("Unexpected copy ID: %v", res.Copy)
	}
}

// ============================================================================
// 4. Action 11: Land Tests
// ============================================================================

func TestRedis_Land_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	card := CardID("card-land-1")
	consumer := ConsumerID("bench:worker-land")
	opts := WriteOptions{Epoch: 1, Actor: "merger"}
	rdb := client.RDB()

	// 1. Push and DealWork
	_ = client.SetConsumerCapacity(ctx, consumer, 4)
	_, _ = client.Push(ctx, card, "main", opts)
	dealRes, err := client.DealWork(ctx, DealWorkParams{Card: card, Consumer: consumer}, opts)
	if err != nil {
		t.Fatalf("DealWork failed: %v", err)
	}

	// 2. Simulate progression: working -> review -> merging
	_ = rdb.ZRem(ctx, "table:streams:1:cell:main:working", string(card)).Err()
	_ = rdb.ZAdd(ctx, "table:streams:1:cell:main:merging", redis.Z{Score: 100, Member: string(card)}).Err()
	_ = rdb.HSet(ctx, MemberKey(string(card)), "where", "merging", "place:streams", "main:merging").Err()

	// Also simulate an open read copy in reads list
	readCopyID := string(card) + ":r1"
	readKey := MemberKey(readCopyID)
	readConsumer := "bench:reader-1"
	_ = rdb.HSet(ctx, readKey,
		"id", readCopyID,
		"epoch", "1",
		"consumer", readConsumer,
		"where", "working",
		"place:fleet", readConsumer+":working",
	).Err()
	_ = rdb.ZAdd(ctx, "table:fleet:1:cell:"+readConsumer+":working", redis.Z{Score: 100, Member: readCopyID}).Err()
	readsJSON, _ := json.Marshal([]string{readCopyID})
	_ = rdb.HSet(ctx, MemberKey(string(card)), "reads", string(readsJSON)).Err()

	// 3. Land
	receipt, err := client.Land(ctx, card, opts)
	if err != nil {
		t.Fatalf("Land failed: %v", err)
	}
	if receipt.Outcome != "OK" {
		t.Errorf("receipt.Outcome = %s, want OK", receipt.Outcome)
	}

	// Verify streams cell: card is in landed cell, removed from merging
	inMerging := rdb.ZScore(ctx, "table:streams:1:cell:main:merging", string(card)).Val()
	if inMerging != 0 {
		t.Errorf("card still in merging cell")
	}
	inLanded := rdb.ZScore(ctx, "table:streams:1:cell:main:landed", string(card)).Val()
	if inLanded <= 0 {
		t.Errorf("card not found in landed cell")
	}

	// Verify primary card hash: where = landed, copy = "", reads = "[]"
	primaryWhere := rdb.HGet(ctx, MemberKey(string(card)), "where").Val()
	if primaryWhere != "landed" {
		t.Errorf("primaryWhere = %s, want landed", primaryWhere)
	}
	primaryCopy := rdb.HGet(ctx, MemberKey(string(card)), "copy").Val()
	if primaryCopy != "" {
		t.Errorf("primaryCopy = %q, want empty", primaryCopy)
	}

	// Verify work copy retired to ok (TerminalIsQuiet)
	workCopyKey := MemberKey(dealRes.Copy.String())
	workCopyWhere := rdb.HGet(ctx, workCopyKey, "where").Val()
	if workCopyWhere != "ok" {
		t.Errorf("workCopyWhere = %s, want ok", workCopyWhere)
	}

	// Verify read copy retired to fail
	readCopyWhere := rdb.HGet(ctx, readKey, "where").Val()
	if readCopyWhere != "fail" {
		t.Errorf("readCopyWhere = %s, want fail", readCopyWhere)
	}
}

func TestRedis_Land_Preconditions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	card := CardID("card-land-pre")
	opts := WriteOptions{Epoch: 1, Actor: "merger"}

	// 1. Non-existent card
	_, err := client.Land(ctx, card, opts)
	if err == nil {
		t.Fatal("expected ErrCardNotFound, got nil")
	}
	if !errors.Is(err, ErrCardNotFound) {
		t.Errorf("expected ErrCardNotFound, got %v", err)
	}

	// 2. Card in waiting (not merging)
	_, _ = client.Push(ctx, card, "main", opts)
	_, err = client.Land(ctx, card, opts)
	if err == nil {
		t.Fatal("expected refusal landing from waiting, got nil")
	}

	// 3. Already landed
	rdb := client.RDB()
	_ = rdb.HSet(ctx, MemberKey(string(card)), "where", "landed").Err()
	_, err = client.Land(ctx, card, opts)
	if err == nil {
		t.Fatal("expected ErrTerminalIsQuiet on already landed, got nil")
	}
	if !errors.Is(err, ErrTerminalIsQuiet) {
		t.Errorf("expected ErrTerminalIsQuiet, got %v", err)
	}
}

func TestRedis_Land_EvalFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := throwaway(t)
	client := NewRedisCardClient(c, WithForceEval(true))

	card := CardID("card-eval-land")
	opts := WriteOptions{Epoch: 1, Actor: "eval-merger"}
	rdb := client.RDB()

	_, _ = client.Push(ctx, card, "main", opts)
	_ = rdb.HSet(ctx, MemberKey(string(card)), "where", "merging").Err()
	_ = rdb.ZAdd(ctx, "table:streams:1:cell:main:merging", redis.Z{Score: 1, Member: string(card)}).Err()

	receipt, err := client.Land(ctx, card, opts)
	if err != nil {
		t.Fatalf("Land via EVAL fallback failed: %v", err)
	}
	if receipt == nil || receipt.Outcome != "OK" {
		t.Errorf("receipt = %+v, want OK", receipt)
	}
}

// ============================================================================
// 5. SequenceRatchet Tests
// ============================================================================

func TestSequenceRatchet_Operations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := throwaway(t)

	sr := NewSequenceRatchet(c, "test:ratchet:seq")
	if sr.Key() != "test:ratchet:seq" {
		t.Errorf("sr.Key() = %s, want test:ratchet:seq", sr.Key())
	}

	// Initial Get is 0
	val, err := sr.Get(ctx)
	if err != nil || val != 0 {
		t.Fatalf("Get = %d, err = %v, want 0", val, err)
	}

	// Advance
	n, err := sr.Advance(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Advance = %d, err = %v, want 1", n, err)
	}

	// Set
	if err := sr.Set(ctx, 10); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	val, _ = sr.Get(ctx)
	if val != 10 {
		t.Fatalf("Get = %d, want 10", val)
	}

	// Ratchet forward: 10 -> 15 (succeeds)
	rVal, err := sr.Ratchet(ctx, 15)
	if err != nil || rVal != 15 {
		t.Fatalf("Ratchet forward failed: %d, err = %v", rVal, err)
	}

	// Ratchet duplicate: 15 -> 15 (fails)
	_, err = sr.Ratchet(ctx, 15)
	if err == nil || !errors.Is(err, ErrMonotonicSequenceViolation) {
		t.Errorf("Expected ErrMonotonicSequenceViolation on duplicate, got %v", err)
	}

	// Ratchet regress: 15 -> 12 (fails)
	_, err = sr.Ratchet(ctx, 12)
	if err == nil || !errors.Is(err, ErrMonotonicSequenceViolation) {
		t.Errorf("Expected ErrMonotonicSequenceViolation on regress, got %v", err)
	}
}

func TestSequenceRatchet_ConcurrentContention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := throwaway(t)

	sr := NewSequenceRatchet(c, "test:ratchet:concurrent")
	const numWorkers = 20
	const incsPerWorker = 50

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incsPerWorker; j++ {
				if _, err := sr.Advance(ctx); err != nil {
					t.Errorf("Advance failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	finalVal, err := sr.Get(ctx)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	expected := uint64(numWorkers * incsPerWorker)
	if finalVal != expected {
		t.Errorf("finalVal = %d, want %d", finalVal, expected)
	}
}

// ============================================================================
// 6. Transaction Engine & Rollback Tests
// ============================================================================

func TestRedis_ExecuteTransaction_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	shadow := NewMemoryCardMachine(1)
	client = NewRedisCardClient(client.RDB(), WithShadowVerifier(shadow))

	ev := JournalMutationEvent{
		Action: ActPush,
		Epoch:  1,
		Actor:  "tx-actor",
		CardID: "card-tx-1",
		Stream: "main",
	}

	tx, err := NewCardTransaction(1, ev, [32]byte{})
	if err != nil {
		t.Fatalf("NewCardTransaction failed: %v", err)
	}

	res, err := client.ExecuteTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("ExecuteTransaction failed: %v", err)
	}

	if res.Seq != 1 {
		t.Errorf("res.Seq = %d, want 1", res.Seq)
	}
	if res.Receipt == nil || res.Receipt.Outcome != "OK" {
		t.Errorf("res.Receipt = %+v", res.Receipt)
	}
	if res.StateHash == [32]byte{} {
		t.Errorf("res.StateHash should not be empty")
	}

	// Verify ratchet advanced to 1
	seq, _ := client.Ratchet().Get(ctx)
	if seq != 1 {
		t.Errorf("ratchet seq = %d, want 1", seq)
	}
}

func TestRedis_ExecuteTransaction_CRC32Violation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	ev := JournalMutationEvent{
		Action: ActPush,
		Epoch:  1,
		Actor:  "tx-actor",
		CardID: "card-tx-crc",
		Stream: "main",
	}

	tx, err := NewCardTransaction(1, ev, [32]byte{})
	if err != nil {
		t.Fatalf("NewCardTransaction failed: %v", err)
	}

	// Corrupt CRC
	tx.PayloadCRC ^= 0xDEADBEEF

	_, err = client.ExecuteTransaction(ctx, tx)
	if err == nil || !errors.Is(err, ErrCRC32Violation) {
		t.Errorf("Expected ErrCRC32Violation, got %v", err)
	}
}

func TestRedis_ExecuteTransaction_StateHashMismatch_Rollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	shadow := NewMemoryCardMachine(1)
	client = NewRedisCardClient(client.RDB(), WithShadowVerifier(shadow))

	ev := JournalMutationEvent{
		Action: ActPush,
		Epoch:  1,
		Actor:  "tx-actor",
		CardID: "card-rollback",
		Stream: "main",
	}

	// Provide an expected state hash that intentionally does not match
	fakeExpectedHash := sha256.Sum256([]byte("wrong-state-hash"))
	tx, err := NewCardTransaction(1, ev, fakeExpectedHash)
	if err != nil {
		t.Fatalf("NewCardTransaction failed: %v", err)
	}

	_, err = client.ExecuteTransaction(ctx, tx)
	if err == nil || !errors.Is(err, ErrStateHashMismatch) {
		t.Fatalf("Expected ErrStateHashMismatch, got %v", err)
	}

	// VERIFY ROLLBACK: card should NOT exist in Redis, ratchet seq should still be 0!
	rdb := client.RDB()
	exists := rdb.Exists(ctx, MemberKey("card-rollback")).Val()
	if exists != 0 {
		t.Errorf("Card was not rolled back in Redis!")
	}
	seq, _ := client.Ratchet().Get(ctx)
	if seq != 0 {
		t.Errorf("Sequence ratchet was not rolled back: %d != 0", seq)
	}
}

// ============================================================================
// 7. Ratchet Step Sequencing & Journal Framing Integration Tests
// ============================================================================

func TestRedis_TransactionWithMonotoneRatchetAndJournal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)

	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "tx_engine.journal")
	jw, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}
	defer jw.Close()

	shadow := NewMemoryCardMachine(1)
	genesisHash := shadow.StateHash()
	monotoneRatchet := NewMonotoneRatchet(
		WithInitialState(0, time.Now(), genesisHash),
		WithStrictContiguous(true),
	)

	client = NewRedisCardClient(
		client.RDB(),
		WithShadowVerifier(shadow),
		WithMonotoneRatchet(monotoneRatchet),
		WithJournalWriter(jw),
	)

	// Transaction 1: SetCapacity
	ev1 := JournalMutationEvent{
		Action:   ActSetCapacity,
		Epoch:    1,
		Actor:    "coord",
		Consumer: "bench:worker-integrated",
		Slots:    4,
	}
	tx1, _ := NewCardTransaction(1, ev1, [32]byte{})
	res1, err := client.ExecuteTransaction(ctx, tx1)
	if err != nil {
		t.Fatalf("Tx 1 failed: %v", err)
	}
	if res1.Frame == nil || res1.Frame.Seq != 1 {
		t.Errorf("Tx 1 Frame missing or wrong seq: %+v", res1.Frame)
	}

	// Transaction 2: Push
	ev2 := JournalMutationEvent{
		Action: ActPush,
		Epoch:  1,
		Actor:  "coord",
		CardID: "card-integrated-1",
		Stream: "main",
	}
	tx2, _ := NewCardTransaction(2, ev2, [32]byte{})
	res2, err := client.ExecuteTransaction(ctx, tx2)
	if err != nil {
		t.Fatalf("Tx 2 failed: %v", err)
	}
	if res2.Frame == nil || res2.Frame.Seq != 2 {
		t.Errorf("Tx 2 Frame missing: %+v", res2.Frame)
	}

	// Transaction 3: DealWork
	ev3 := JournalMutationEvent{
		Action:   ActDealWork,
		Epoch:    1,
		Actor:    "coord",
		CardID:   "card-integrated-1",
		Consumer: "bench:worker-integrated",
		Score:    5.0,
	}
	tx3, _ := NewCardTransaction(3, ev3, [32]byte{})
	res3, err := client.ExecuteTransaction(ctx, tx3)
	if err != nil {
		t.Fatalf("Tx 3 failed: %v", err)
	}
	if res3.Frame == nil || res3.Frame.Seq != 3 {
		t.Errorf("Tx 3 Frame missing: %+v", res3.Frame)
	}

	// Verify MonotoneRatchet stepped to 3
	if monotoneRatchet.CurrentSeq() != 3 {
		t.Errorf("MonotoneRatchet CurrentSeq = %d, want 3", monotoneRatchet.CurrentSeq())
	}
	if monotoneRatchet.CurrentStateHash() != shadow.StateHash() {
		t.Errorf("MonotoneRatchet state hash mismatch with shadow")
	}

	// Verify Journal has exactly 3 frames
	_ = jw.Sync()
	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	frames, err := reader.Scan()
	if err != nil {
		t.Fatalf("reader.Scan failed: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("Journal total frames = %d, want 3", len(frames))
	}
	for i, f := range frames {
		expectedSeq := uint64(i + 1)
		if f.Seq != expectedSeq {
			t.Errorf("Frame %d sequence mismatch: got %d, want %d", i, f.Seq, expectedSeq)
		}
	}
}
