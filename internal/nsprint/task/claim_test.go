package task_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// startRedis starts a throwaway redis-server for a control sprint. The same
// shape as store_test.go: skip with the reason when the binary is absent.
func startRedis(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	port := strings.TrimPrefix(addr, "127.0.0.1:")
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("throwaway redis did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func controlRedis(t *testing.T) (*store.Store, *redis.Client) {
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

// TestControl03PushIsCreateOnly is #2756 control 3: push, done, the same push
// stays closed at exit 0 CLOSED, and a push at a new head creates a distinct
// review identity.
func TestControl03PushIsCreateOnly(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-abcdef01"
	client.HSet(ctx, "s:"+sprint, "status", "open")

	seedFriend(t, client, "ctl-a", 4)

	push := task.PushRequest{
		Sprint: sprint, ID: "t1", Kind: task.KindWork, Title: "a task",
		Effects: task.EffectsNone, PayloadSHA: "p1", To: "ctl-a",
	}
	got, err := task.Push(ctx, st, push)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if got != task.PushCreated {
		t.Fatalf("first push = %s; want CREATED", got)
	}
	firstReceipts, err := receipts(ctx, client, "s:"+sprint+":log", "task push", "t1")
	if err != nil || firstReceipts != 1 {
		t.Fatalf("first push receipts = %d, %v; want one", firstReceipts, err)
	}

	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "t1", As: "ctl-a"})
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if !ok {
		t.Fatal("the only task must be claimable")
	}
	if claim.Attempt != 1 || !strings.HasPrefix(claim.Token, "1.") {
		t.Fatalf("claim attempt=%d token=%q; want attempt 1 and a 1. token", claim.Attempt, claim.Token)
	}

	if got, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t1", Token: claim.Token, Evidence: "https://example.test/evidence/t1",
	}); err != nil || got != task.DoneClosed {
		t.Fatalf("done = %s, %v; want DONE", got, err)
	}
	if got, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t1", Token: "1.wrong", Evidence: "https://example.test/evidence/t1",
	}); err != nil || got != task.DoneFenced || got.ExitCode() != 3 {
		t.Fatalf("wrong token after done = %s, %v; want FENCED exit 3", got, err)
	}
	if got, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t1", Token: claim.Token, Evidence: "https://example.test/evidence/t1",
	}); err != nil || got != task.DoneRepeat {
		t.Fatalf("repeated done = %s, %v; want CLOSED", got, err)
	}

	// The same push of the closed task stays closed and writes nothing.
	again, err := task.Push(ctx, st, push)
	if err != nil {
		t.Fatalf("re-push: %v", err)
	}
	if again != task.PushClosed {
		t.Fatalf("same push of a closed task = %s; want CLOSED", again)
	}
	if again.ExitCode() != 0 {
		t.Fatalf("CLOSED exit = %d; want 0", again.ExitCode())
	}
	state, err := client.HGet(ctx, "s:"+sprint+":task:t1", "state").Result()
	if err != nil || state != "closed" {
		t.Fatalf("same push changed terminal state to %q: %v", state, err)
	}
	pushReceipts, err := receipts(ctx, client, "s:"+sprint+":log", "task push", "t1")
	if err != nil || pushReceipts != 1 {
		t.Fatalf("same push receipts = %d, %v; want one", pushReceipts, err)
	}

	// A push at a new head is a distinct review identity, not a reopen.
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	idA := task.ReviewID("repo", 42, headA, "ctl-a")
	idB := task.ReviewID("repo", 42, headB, "ctl-a")
	if idA == idB {
		t.Fatalf("review id did not change with the head: %q", idA)
	}
	reviewA := task.PushRequest{
		Sprint: sprint, ID: idA, Kind: task.KindReview, Title: "review",
		Effects: task.EffectsNone, Repo: "repo", PR: 42, Head: headA,
	}
	reviewB := reviewA
	reviewB.ID = idB
	reviewB.Head = headB
	client.HSet(ctx, "s:"+sprint+":pr:repo:42", "head", headA)
	if got, err := task.Push(ctx, st, reviewA); err != nil || got != task.PushCreated {
		t.Fatalf("review push at head A = %s, %v; want CREATED", got, err)
	}
	client.HSet(ctx, "s:"+sprint+":pr:repo:42", "head", headB)
	if got, err := task.Push(ctx, st, reviewB); err != nil || got != task.PushCreated {
		t.Fatalf("review push at head B = %s, %v; want CREATED", got, err)
	}
}

// TestControl04ConcurrentTakesOneOwner is #2756 control 4: two concurrent
// takes of one task give exactly one owner and one receipt.
func TestControl04ConcurrentTakesOneOwner(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-12345678"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	seedFriend(t, client, "ctl-b", 2)

	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "t2", Kind: task.KindWork, Title: "second",
		Effects: task.EffectsNone, PayloadSHA: "p2", To: "ctl-b",
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push = %s, %v; want CREATED", got, err)
	}

	var wg sync.WaitGroup
	claims := make(chan task.Claim, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			available, err := task.TakeAvailable(ctx, st, "ctl-b", "", "", 0, "ctl-b", "")
			if err != nil {
				errs <- err
				return
			}
			for _, claim := range available {
				claims <- claim
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent take: %v", err)
	}
	owners := 0
	var winner task.Claim
	for claim := range claims {
		owners++
		winner = claim
	}
	if owners != 1 {
		t.Fatalf("owners = %d; want exactly one", owners)
	}

	owner, err := client.HGet(ctx, "s:"+sprint+":task:t2", "owner").Result()
	if err != nil || owner != "ctl-b" {
		t.Fatalf("task owner = %q, %v; want ctl-b", owner, err)
	}
	takes, err := receipts(ctx, client, "s:"+sprint+":log", "task take", "t2")
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	if takes != 1 {
		t.Fatalf("take receipts = %d; want exactly one", takes)
	}
	entries, err := client.XRange(ctx, "s:"+sprint+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(winner.Token))
	wantSHA := hex.EncodeToString(sum[:])[:12]
	for _, entry := range entries {
		if entry.Values["kind"] == "task take" {
			if got := entry.Values["token_sha"]; got != wantSHA {
				t.Fatalf("take receipt token_sha = %v; want %s", got, wantSHA)
			}
		}
	}
	for _, id := range []string{"t3", "t4"} {
		if got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: id,
			Effects: task.EffectsNone, To: "ctl-b",
		}); err != nil || got != task.PushCreated {
			t.Fatalf("push %s = %s, %v", id, got, err)
		}
	}
	remaining, err := task.TakeAvailable(ctx, st, "ctl-b", "", "", 0, "ctl-b", "")
	if err != nil || len(remaining) != 1 {
		t.Fatalf("one free slot should claim one task: %d, %v", len(remaining), err)
	}
	full, err := task.TakeAvailable(ctx, st, "ctl-b", "", "", 0, "ctl-b", "")
	if err != nil || len(full) != 0 {
		t.Fatalf("full friend claimed %d tasks: %v", len(full), err)
	}
}

func seedFriend(t *testing.T, client *redis.Client, friend string, slots int) {
	t.Helper()
	ctx := context.Background()
	if err := client.SAdd(ctx, "friends", friend).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":desired", "slots", slots, "paused", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":beat", "host", "fixture").Err(); err != nil {
		t.Fatal(err)
	}
}

// receipts counts log entries with the given kind and id.
func receipts(ctx context.Context, client *redis.Client, stream, kind, id string) (int, error) {
	messages, err := client.XRange(ctx, stream, "-", "+").Result()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, message := range messages {
		k, _ := message.Values["kind"].(string)
		v, _ := message.Values["id"].(string)
		if k == kind && v == id {
			count++
		}
	}
	return count, nil
}
