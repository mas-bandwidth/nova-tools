package capacity_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func redisControl(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load Redis Functions: %v", err)
	}
	return store.New(client), client
}

func TestControl27RedisAtomicCeiling(t *testing.T) {
	st, c := redisControl(t)
	ctx := context.Background()
	if _, err := capacity.SetMachine(ctx, st, "ctl-machine", 64, 64, 128, "test", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := capacity.SetBench(ctx, st, "bench-zero", "ctl-machine", 0, "test", ""); err != nil {
		t.Fatal(err)
	}
	if c.SIsMember(ctx, "benches", "bench-zero").Val() != true {
		t.Fatal("capacity bench did not register bench")
	}
	c.SAdd(ctx, "friends", "a", "b")
	if _, err := capacity.SetFriend(ctx, st, "a", "ctl-machine", 32, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.SetFriend(ctx, st, "b", "ctl-machine", 32, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.SetFriend(ctx, st, "b", "ctl-machine", 33, "test", ""); err == nil || !strings.Contains(err.Error(), "CEILING ctl-machine 65/64") {
		t.Fatalf("b=33: %v", err)
	}
	if got := c.HGet(ctx, "friend:b:desired", "slots").Val(); got != "32" {
		t.Fatalf("refusal changed b slots to %q", got)
	}
	if _, err := capacity.SetFriend(ctx, st, "a", "ctl-machine", 64, "test", ""); err == nil || !strings.Contains(err.Error(), "CEILING ctl-machine 96/64") {
		t.Fatalf("a=64: %v", err)
	}
	if got := c.HGet(ctx, "friend:a:desired", "slots").Val(); got != "32" {
		t.Fatalf("refusal changed a slots to %q", got)
	}
	if count := c.XLen(ctx, "cap:log").Val(); count != 4 {
		t.Fatalf("capacity receipts=%d want 4 successful writes only", count)
	}

	// A width edit is the capacity writer; the read is the one lease ledger (#3998).
	c.ZAdd(ctx, "friend:a:cards:working", redis.Z{Member: "one"})
	c.ZAdd(ctx, "friend:a:cards:working", redis.Z{Member: "two"})
	width, err := task.GetWidth(ctx, st, "a")
	if err != nil {
		t.Fatal(err)
	}
	if width.Desired != 32 || width.Leased != 2 || width.Free != 30 {
		t.Fatalf("width=%+v", width)
	}
	if _, err := capacity.SetMachine(ctx, st, "ctl-machine", 64, 0, 0, "test", ""); err != nil {
		t.Fatal(err)
	}
	if cores := c.HGet(ctx, "machine:ctl-machine:ceiling", "cores").Val(); cores != "64" {
		t.Fatalf("slot-only update erased measured cores: %q", cores)
	}
	if _, err := capacity.SetFriend(ctx, st, "new", "ctl-machine", 0, "test", ""); err != nil {
		t.Fatalf("first capacity write did not register friend: %v", err)
	}
	if !c.SIsMember(ctx, "friends", "new").Val() {
		t.Fatal("first capacity write omitted friends registry")
	}

	// Two concurrent raises must not both pass the atomic Lua guard.
	if _, err := capacity.SetFriend(ctx, st, "a", "ctl-machine", 31, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.SetFriend(ctx, st, "b", "ctl-machine", 31, "test", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := capacity.SetFriend(ctx, st, name, "ctl-machine", 33, "test", "")
			results <- err
		}(name)
	}
	wg.Wait()
	close(results)
	ok, refused := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if strings.Contains(err.Error(), "CEILING") {
			refused++
		} else {
			t.Fatalf("unexpected raise error: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("concurrent raises allowed=%d refused=%d", ok, refused)
	}
	a := c.HGet(ctx, "friend:a:desired", "slots").Val()
	b := c.HGet(ctx, "friend:b:desired", "slots").Val()
	if !((a == "33" && b == "31") || (a == "31" && b == "33")) {
		t.Fatalf("final slots a=%s b=%s", a, b)
	}
}

func TestControl22RedisStaleQueueAndFriendIsolation(t *testing.T) {
	st, c := redisControl(t)
	ctx := context.Background()
	s := "control-22334455"
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "s:"+s+":open:a", redis.Z{Member: "live"}, redis.Z{Member: "closed"})
	c.HSet(ctx, "task:live", "state", "open", "owner", "")
	c.HSet(ctx, "task:closed", "state", "closed", "owner", "a")
	c.SAdd(ctx, "s:"+s+":idx:task:claimed", "other")
	c.HSet(ctx, "task:other", "state", "claimed", "owner", "b")
	c.SAdd(ctx, "s:"+s+":idx:task:working", "mine")
	c.HSet(ctx, "task:mine", "state", "working", "owner", "a")
	rows, err := task.ListStore(ctx, st, task.ListRequest{As: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "live" || rows[1].ID != "mine" {
		t.Fatalf("live rows=%+v", rows)
	}
}
