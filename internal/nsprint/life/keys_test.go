package life_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/beat"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/redis/go-redis/v9"
)

// TestKeysDoNotExpire is nova-tools #3878's DONE-WHEN for the nova_sprint
// beats, against a real Redis and the real library: after one beat of each
// kind -- hello and beat (presence.lua), the row (ns_friend_row), the seat's
// serve beat (friend_serve.lua) and the bench beat -- PTTL on friend:<x>:beat,
// the friend's table row friend:<x> and bench:<b>:beat answers -1, and each
// carries the window it promises as stale_ms. A TTL an older binary left on
// the row is removed by the next write. A beat whose at is older than its
// window is down to every reader (the row's up, beat, hello's seat fence,
// bench-up), with its hash still in the store.
func TestKeysDoNotExpire(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	client.SAdd(ctx, "friends", "emma", "stella")
	client.HSet(ctx, "friend:emma:desired", "slots", 4, "machine", "studio", "paused", "0")

	pttl := func(key string) time.Duration {
		t.Helper()
		d, err := client.PTTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	noTTL := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			if d := pttl(k); d != -1 {
				t.Fatalf("PTTL %s = %v; want -1 (keys do not expire)", k, d)
			}
		}
	}

	// presence.lua: hello, beat, and the row the row loop writes.
	if _, err := life.Hello(ctx, st, life.HelloRequest{As: "emma", Slots: -1, Host: "studio", Session: "e-1", Actor: "emma"}); err != nil {
		t.Fatal(err)
	}
	if err := life.Beat(ctx, st, life.Presence{Friend: "emma", Host: "studio", Session: "e-1"}); err != nil {
		t.Fatal(err)
	}
	// An older friend_serve gave the row a TTL; the row loop's write takes it off.
	client.HSet(ctx, "friend:emma", "at", "x")
	client.PExpire(ctx, "friend:emma", time.Minute)
	row, err := life.FriendRow(ctx, st, life.FriendRowRequest{Friend: "emma", Sprint: "s1"})
	if err != nil || !row.Up {
		t.Fatalf("row: %+v %v; want up", row, err)
	}
	noTTL("friend:emma:beat", "friend:emma")
	if got := client.HGet(ctx, "friend:emma:beat", beat.FieldStale).Val(); got != "5000" {
		t.Fatalf("friend:emma:beat stale_ms = %q; want 5000", got)
	}

	// friend_serve.lua: the seat's beat writes the beat and the row.
	client.HSet(ctx, "friend:stella", "at", "x")
	client.PExpire(ctx, "friend:stella", time.Minute)
	stamp := time.Now().UTC().Format(time.RFC3339)
	res, err := client.FCall(ctx, life.FunctionServeBeat, nil, "stella", "s-1", stamp, "0", "2", "claude", "studio", "stella").StringSlice()
	if err != nil || len(res) == 0 || res[0] != "OK" {
		t.Fatalf("serve beat: %v %v", res, err)
	}
	noTTL("friend:stella:beat", "friend:stella")
	if got := client.HGet(ctx, "friend:stella", beat.FieldStale).Val(); got != "5000" {
		t.Fatalf("friend:stella stale_ms = %q; want 5000", got)
	}
	if d := pttl("friend:stella:serve"); d <= 0 {
		t.Fatalf("friend:stella:serve PTTL %v; the seat lock is a lease and keeps its TTL", d)
	}

	// presence.lua: the bench beat. The owner key and the live set stay leases.
	if r, err := life.BenchBeat(ctx, st, life.BenchRequest{Bench: "b1", Host: "b1", Session: "sess", Live: []string{"c1"}, Actor: "b1", TTL: 7 * time.Second}); err != nil || !r.Accepted {
		t.Fatalf("bench beat: %+v %v", r, err)
	}
	noTTL("bench:b1:beat")
	if got := client.HGet(ctx, "bench:b1:beat", beat.FieldStale).Val(); got != "7000" {
		t.Fatalf("bench:b1:beat stale_ms = %q; want 7000", got)
	}
	if d := pttl("bench:b1:owner"); d <= 0 {
		t.Fatalf("bench:b1:owner PTTL %v; the owner is a lease and keeps its TTL", d)
	}

	// Staleness is at against stale_ms: age every beat past its window.
	now := serverMs(t, client)
	old := strconv.FormatInt(now-60_000, 10)
	for _, k := range []string{"friend:emma:beat", "friend:stella:beat", "bench:b1:beat"} {
		client.HSet(ctx, k, "at", old)
	}
	for _, k := range []string{"friend:emma:beat", "bench:b1:beat"} {
		if live, err := beat.LiveNow(ctx, client, k); err != nil || live {
			t.Fatalf("%s aged past its window reads live=%v err=%v; want down", k, live, err)
		}
	}
	row, err = life.FriendRow(ctx, st, life.FriendRowRequest{Friend: "emma", Sprint: "s1"})
	if err != nil || row.Up {
		t.Fatalf("row over a stale beat: %+v %v; want up=false", row, err)
	}
	if err := life.Beat(ctx, st, life.Presence{Friend: "emma", Host: "studio", Session: "e-1"}); err == nil {
		t.Fatal("beat over a stale beat succeeded; want DOWN (hello again)")
	}
	// A stale session no longer holds the seat: a new session's hello is up.
	if r, err := life.Hello(ctx, st, life.HelloRequest{As: "emma", Slots: -1, Host: "studio", Session: "e-2", Actor: "emma"}); err != nil || !r.Up {
		t.Fatalf("hello by a new session over a stale beat: %+v %v; want up", r, err)
	}
	// And the stale seat's serve beat yields to a new serve session once
	// its lock (a lease) has lapsed.
	client.Del(ctx, "friend:stella:serve")
	res, err = client.FCall(ctx, life.FunctionServeBeat, nil, "stella", "s-2", stamp, "0", "2", "claude", "studio", "stella").StringSlice()
	if err != nil || len(res) < 2 || res[0] != "OK" || res[1] != "0" {
		t.Fatalf("serve beat by a new session over a stale beat: %v %v; want OK, was down", res, err)
	}
	// The bench's next beat is a return: bench-up is logged again.
	before := client.XLen(ctx, "cap:log").Val()
	if r, err := life.BenchBeat(ctx, st, life.BenchRequest{Bench: "b1", Host: "b1", Session: "sess", Actor: "b1", TTL: 7 * time.Second}); err != nil || !r.Accepted {
		t.Fatalf("bench beat: %+v %v", r, err)
	}
	if n := client.XLen(ctx, "cap:log").Val(); n != before+1 {
		t.Fatalf("cap:log grew by %d on a stale bench's beat; want one bench-up", n-before)
	}
}

func serverMs(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	tm, err := c.Time(context.Background()).Result()
	if err != nil {
		t.Fatal(err)
	}
	return tm.UnixMilli()
}
