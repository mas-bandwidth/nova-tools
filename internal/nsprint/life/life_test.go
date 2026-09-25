package life_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// startRedis starts a throwaway redis-server for a control sprint. Same shape
// as internal/nsprint/task/claim_test.go: internal/nsprint/testutil fails a
// missing binary under NOVA_CI and skips otherwise.
func startRedis(t *testing.T) string {
	t.Helper()
	return testutil.Start(t)
}

// controlRedis starts one throwaway redis with the real nova_sprint library
// loaded and returns its address so a second independent client can be built.
func controlRedis(t *testing.T) (*store.Store, *redis.Client, string) {
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client, addr
}

// TestControl15FriendReturnTakesWork is #2756 control 15 against a real Redis
// and the real Lua functions: a registered-but-down friend owns open ready
// work, one hello takes that work with no manual take, the fence token is
// preserved for the child, and bye leaves the remaining queue in place.
func TestControl15FriendReturnTakesWork(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	const sprint, friend = "s1", "walter"

	if err := client.HSet(ctx, "s:"+sprint, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	// Registered friend with desired slots but no beat: it is down.
	if err := client.SAdd(ctx, "friends", friend).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":desired",
		"slots", 1, "machine", "bench-a", "paused", 0, "at", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "machine:bench-a:ceiling", "slots", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if exists := client.Exists(ctx, "friend:"+friend+":beat").Val(); exists != 0 {
		t.Fatalf("seed friend must start down, but a beat exists")
	}
	for _, id := range []string{"w1", "w2"} {
		if got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: id,
			Effects: task.EffectsNone, PayloadSHA: id, To: friend,
		}); err != nil || got != task.PushCreated {
			t.Fatalf("push assigned work while friend is down: %s, %v", got, err)
		}
	}

	// No manual take: Hello must claim the friend's own queued work.
	res, err := life.Hello(ctx, st, life.HelloRequest{
		Sprint: sprint, As: friend, Slots: 1,
		Harness: "codex", Host: "bench-a", Session: "sess-1",
		Actor: friend, Idem: "hello-1",
	})
	if err != nil {
		t.Fatalf("friend hello: %v", err)
	}
	if !res.Up {
		t.Fatalf("friend hello did not come up: %+v", res)
	}
	if len(res.Claims) != 1 {
		t.Fatalf("return took %d tasks, want 1 without a manual deal", len(res.Claims))
	}
	claim := res.Claims[0]
	if claim.Sprint != sprint || claim.ID == "" || claim.Attempt != 1 || claim.Token == "" {
		t.Fatalf("claim is not fenced: %+v", claim)
	}
	if stored, _ := client.HGet(ctx, "s:"+sprint+":task:"+claim.ID, "token").Result(); stored != claim.Token {
		t.Fatalf("fence token not preserved for the child: got %q want %q", stored, claim.Token)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":task:"+claim.ID, "state").Result(); state != "claimed" && state != "working" {
		t.Fatalf("task state %q, want claimed or working", state)
	}
	if owner, _ := client.HGet(ctx, "s:"+sprint+":task:"+claim.ID, "owner").Result(); owner != friend {
		t.Fatalf("task owner %q, want %q", owner, friend)
	}
	if beat := client.Exists(ctx, "friend:"+friend+":beat").Val(); beat != 1 {
		t.Fatalf("hello did not write a beat; friend is still down")
	}
	if wake, err := life.PollWake(ctx, st, friend); err != nil || !strings.Contains(wake, "friend-return") {
		t.Fatalf("hello wake = %q, %v; want a zero-token return wake", wake, err)
	}
	if wake, err := life.PollWake(ctx, st, friend); err != nil || wake != "" {
		t.Fatalf("consumed wake repeated: %q, %v", wake, err)
	}
	if _, err := life.Hello(ctx, st, life.HelloRequest{
		As: friend, Slots: -1, Host: "bench-a", Session: "other-session", Actor: friend,
	}); err == nil || !strings.Contains(err.Error(), "BUSY") {
		t.Fatalf("second live hello = %v, want BUSY", err)
	}
	if left, _ := client.ZCard(ctx, "s:"+sprint+":open:"+friend).Result(); left != 1 {
		t.Fatalf("assigned queue has %d tasks before bye, want 1 (slots cap)", left)
	}

	// Bye stops presence and leaves the remaining assigned work in place.
	was, err := life.Bye(ctx, st, friend, "control15", "bye-1")
	if err != nil {
		t.Fatalf("friend bye: %v", err)
	}
	if !was {
		t.Fatalf("bye did not report the registered friend")
	}
	if !client.SIsMember(ctx, "friends", friend).Val() {
		t.Fatalf("bye removed the registered friend, preventing offline assignment")
	}
	if beat := client.Exists(ctx, "friend:"+friend+":beat").Val(); beat != 0 {
		t.Fatalf("bye left the friend's beat")
	}
	if left, _ := client.ZCard(ctx, "s:"+sprint+":open:"+friend).Result(); left != 1 {
		t.Fatalf("bye removed assigned work: %d left, want 1", left)
	}
	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "w3", Kind: task.KindWork, Title: "queued while down",
		Effects: task.EffectsNone, PayloadSHA: "w3", To: friend,
	}); err != nil || got != task.PushCreated {
		t.Fatalf("offline registered friend cannot be assigned new work: %s, %v", got, err)
	}
}

// TestBenchBeatSingleInstance is #2756 v5 single-instance for a bench: two
// independent clients/instances beat the same bench, the first wins and the
// second refuses, and once the owner and beat expire a new instance recovers.
func TestBenchBeatSingleInstance(t *testing.T) {
	stA, clientA, addr := controlRedis(t)
	ctx := context.Background()

	clientB := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = clientB.Close() })
	stB := store.New(clientB)

	first, err := life.BenchBeat(ctx, stA, life.BenchRequest{
		Bench: "b1", Host: "host-a", Session: "sess-a",
		Live: []string{"card-1", "card-2"}, Actor: "bench-a", Idem: "b1-1",
	})
	if err != nil {
		t.Fatalf("first bench beat: %v", err)
	}
	if !first.Accepted || first.Owner != "sess-a" {
		t.Fatalf("first beat not accepted by its own session: %+v", first)
	}
	if up := clientA.XLen(ctx, "cap:log").Val(); up < 1 {
		t.Fatalf("absence-to-presence did not log bench-up")
	}

	second, err := life.BenchBeat(ctx, stB, life.BenchRequest{
		Bench: "b1", Host: "host-b", Session: "sess-b",
		Live: []string{"card-9"}, Actor: "bench-b", Idem: "b1-2",
	})
	if err != nil {
		t.Fatalf("second bench beat: %v", err)
	}
	if second.Accepted {
		t.Fatalf("a second independent instance was allowed to own one bench")
	}
	if second.Owner != "sess-a" {
		t.Fatalf("refusal did not name the live owner: %+v", second)
	}

	// Simulate the owner stalling: the beat and fenced owner key expire.
	if err := clientA.PExpire(ctx, "bench:b1:beat", time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	if err := clientA.PExpire(ctx, "bench:b1:owner", time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	recovered, err := life.BenchBeat(ctx, stB, life.BenchRequest{
		Bench: "b1", Host: "host-b", Session: "sess-b",
		Live: []string{"card-9"}, Actor: "bench-b", Idem: "b1-3",
	})
	if err != nil {
		t.Fatalf("recovery bench beat: %v", err)
	}
	if !recovered.Accepted || recovered.Owner != "sess-b" {
		t.Fatalf("stale owner did not permit recovery: %+v", recovered)
	}
	if live, _ := clientB.SMembers(ctx, "bench:b1:live").Result(); len(live) != 1 || live[0] != "card-9" {
		t.Fatalf("live set not renewed by the recovering beat: %v", live)
	}
}

// TestBenchBeatKeyHasTTL (#3372): every bench beat writes bench:<b>:beat,
// :owner and :live with a TTL of 3 x the beat interval (BenchBeatTTL), so a
// dead loop's row leaves the store on its own; an explicit TTL is honoured.
func TestBenchBeatKeyHasTTL(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	if life.BenchBeatTTL != 3*life.BeatInterval {
		t.Fatalf("BenchBeatTTL %s, want 3 x %s", life.BenchBeatTTL, life.BeatInterval)
	}
	for _, tc := range []struct {
		bench string
		ttl   time.Duration
		want  time.Duration
	}{
		{"b1", 0, life.BenchBeatTTL},
		{"b2", 7 * time.Second, 7 * time.Second},
	} {
		res, err := life.BenchBeat(ctx, st, life.BenchRequest{
			Bench: tc.bench, Host: "host-" + tc.bench, Session: "sess-" + tc.bench,
			Live: []string{"card-1"}, Actor: "bench", TTL: tc.ttl,
		})
		if err != nil || !res.Accepted {
			t.Fatalf("%s beat: %+v %v", tc.bench, res, err)
		}
		for _, key := range []string{"bench:" + tc.bench + ":beat", "bench:" + tc.bench + ":owner", "bench:" + tc.bench + ":live"} {
			ttl, err := client.PTTL(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			if ttl <= tc.want-time.Second || ttl > tc.want {
				t.Fatalf("%s PTTL %s, want (%s, %s]", key, ttl, tc.want-time.Second, tc.want)
			}
		}
	}
}

func TestFriendHelloPreservesConfiguredCapacity(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	client.HSet(ctx, "machine:studio:ceiling", "slots", 64)
	client.SAdd(ctx, "friends", "a", "b")
	client.HSet(ctx, "friend:a:desired", "slots", 32, "machine", "studio", "paused", "0")
	client.HSet(ctx, "friend:b:desired", "slots", 32, "machine", "studio", "paused", "0")

	for _, req := range []life.HelloRequest{
		{As: "a", Slots: 64, Host: "studio", Session: "a-1", Actor: "a"},
		{As: "b", Slots: 33, Host: "studio", Session: "b-1", Actor: "b"},
	} {
		res, err := life.Hello(ctx, st, req)
		if err != nil || res.Slots != 32 {
			t.Fatalf("hello %s slots=%d: got %+v, %v; want configured 32", req.As, req.Slots, res, err)
		}
		if got := client.HGet(ctx, "friend:"+req.As+":desired", "slots").Val(); got != "32" {
			t.Fatalf("hello changed %s desired to %s", req.As, got)
		}
		if _, err := life.Bye(ctx, st, req.As, req.As, ""); err != nil {
			t.Fatal(err)
		}
	}
	res, err := life.Hello(ctx, st, life.HelloRequest{
		As: "a", Slots: -1, Host: "studio.local", Machine: "studio", Session: "a-preserve", Actor: "a",
	})
	if err != nil || res.Slots != 32 {
		t.Fatalf("hello without --slots must preserve configured 32 slots: %+v, %v", res, err)
	}
}

func TestHelloNeverWritesDesired(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	client.SAdd(ctx, "friends", "f")
	client.HSet(ctx, "friend:f:desired", "slots", 7, "machine", "m", "paused", 0, "at", 1)
	client.HSet(ctx, "machine:m:ceiling", "slots", 40)

	if _, err := life.Hello(ctx, st, life.HelloRequest{
		As: "f", Slots: 3, Host: "m", Session: "s", Actor: "f",
	}); err != nil {
		t.Fatal(err)
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "7" {
		t.Fatalf("hello rewrote desired slots=%q, want 7", got)
	}
}

func TestHelloUnregisteredRefuses(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	client.HSet(ctx, "machine:m:ceiling", "slots", 40)
	if _, err := life.Hello(ctx, st, life.HelloRequest{
		As: "new", Slots: 1, Host: "m", Session: "s", Actor: "new",
	}); err == nil || !strings.Contains(err.Error(), "UNREGISTERED new: nova-sprint capacity friend") {
		t.Fatalf("hello error=%v", err)
	}
	if client.SIsMember(ctx, "friends", "new").Val() || client.Exists(ctx, "friend:new:desired").Val() != 0 {
		t.Fatal("unregistered hello wrote capacity state")
	}
}
