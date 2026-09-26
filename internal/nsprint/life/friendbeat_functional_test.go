//go:build functional

package life_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFriendBeatWritesBeatAndRenewsLeasesInOneRoundTrip (#4233): one friend
// beat is one pipeline that writes friend:<f>:beat (host, at ms, load1,
// ncpu, cpu, harness), removes any TTL a hello loop left on it (keys do not
// expire) and renews the lease of every copy in friend:<f>:cards:working
// through ns_cm_beat in the same round trip. The friend:<f> row is not
// written (ns_friend_row is its one writer). An empty measurement is not
// written; a missing friend or host is refused.
func TestFriendBeatWritesBeatAndRenewsLeasesInOneRoundTrip(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	st := store.New(client)
	client.HSet(ctx, "friend:rowan:beat", "session", "s1", "at", "1")
	client.PExpire(ctx, "friend:rowan:beat", 5*time.Second)
	for i, id := range []string{"p1~1", "p2~1"} {
		client.ZAdd(ctx, "friend:rowan:cards:working", redis.Z{Score: float64(i + 1), Member: id})
		client.HSet(ctx, "task:"+id, "consumer", "friend:rowan", "where", "working", "lease_until", "1")
	}
	at := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

	trips := client.PoolStats().Hits + client.PoolStats().Misses
	res, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: "Rowan", Host: "laptop", Load1: "0.50", NCPU: 8, CPU: "12.5", At: at})
	if err != nil {
		t.Fatal(err)
	}
	if got := client.PoolStats().Hits + client.PoolStats().Misses - trips; got != 1 {
		t.Fatalf("friend beat took %d connections from the pool, want 1 (one round trip)", got)
	}
	if res.Friend != "rowan" || res.AtMS != at.UnixMilli() || res.Working != 2 || res.LeaseUntil <= time.Now().Add(time.Minute).UnixMilli() {
		t.Fatalf("result %+v", res)
	}
	for _, id := range []string{"p1~1", "p2~1"} {
		if got := client.HGet(ctx, "task:"+id, "lease_until").Val(); got != strconv.FormatInt(res.LeaseUntil, 10) {
			t.Fatalf("task:%s lease_until = %q, want %d", id, got, res.LeaseUntil)
		}
	}
	beat := client.HGetAll(ctx, "friend:rowan:beat").Val()
	want := map[string]string{"session": "s1", "host": "laptop", "at": strconv.FormatInt(at.UnixMilli(), 10),
		"harness": life.FriendBeatHarness, "load1": "0.50", "ncpu": "8", "cpu": "12.5"}
	for k, v := range want {
		if beat[k] != v {
			t.Fatalf("friend:rowan:beat %s = %q, want %q (%v)", k, beat[k], v, beat)
		}
	}
	if len(beat) != len(want) {
		t.Fatalf("friend:rowan:beat = %v", beat)
	}
	if ttl := client.TTL(ctx, "friend:rowan:beat").Val(); ttl > 0 {
		t.Fatalf("friend:rowan:beat keeps a TTL of %s; keys do not expire", ttl)
	}
	if n := client.Exists(ctx, "friend:rowan").Val(); n != 0 {
		t.Fatalf("friend:rowan row written by the beat; ns_friend_row is its one writer")
	}

	// no measurement and nothing working: the fields are left alone, lease 0
	res, err = life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: "emma", Host: "studio", At: at})
	if err != nil || res.Working != 0 || res.LeaseUntil != 0 {
		t.Fatalf("emma: %+v %v", res, err)
	}
	if beat := client.HGetAll(ctx, "friend:emma:beat").Val(); len(beat) != 3 || beat["host"] != "studio" || beat["harness"] != life.FriendBeatHarness {
		t.Fatalf("friend:emma:beat = %v", beat)
	}
	for _, bad := range []life.FriendBeatRequest{{Host: "h"}, {Friend: "f"}, {Friend: "friend:f", Host: "h"}, {Friend: "f", Host: "a b"}} {
		if _, err := life.FriendBeat(ctx, st, bad); err == nil {
			t.Fatalf("%+v: no refusal", bad)
		}
	}
	if _, err := life.FriendBeat(ctx, nil, life.FriendBeatRequest{Friend: "f", Host: "h"}); err == nil {
		t.Fatal("nil store: no refusal")
	}
}
