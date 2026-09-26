package life_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TestFriendBeatWritesBeatAndRowAndFindsWorking (#4233): one friend beat is
// one pipeline that writes friend:<f>:beat (host, at ms, load1, ncpu, cpu,
// harness), removes any TTL a hello loop left on it (keys do not expire),
// writes the friend:<f> row's at/up/host the deal duty reads, and returns
// the copies the friend holds working so the verb renews their leases. An
// empty measurement is not written; a missing friend or host is refused.
func TestFriendBeatWritesBeatAndRowAndFindsWorking(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	st := store.New(client)
	ctx := context.Background()
	mr.HSet("friend:rowan:beat", "session", "s1", "at", "1")
	mr.SetTTL("friend:rowan:beat", 5*time.Second)
	mr.ZAdd("friend:rowan:cards:working", 1, "p1~1")
	mr.ZAdd("friend:rowan:cards:working", 2, "p2~1")
	at := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

	res, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: "Rowan", Host: "laptop", Load1: "0.50", NCPU: 8, CPU: "12.5", At: at})
	if err != nil {
		t.Fatal(err)
	}
	if res.Friend != "rowan" || res.AtMS != at.UnixMilli() || len(res.Working) != 2 || res.Working[0] != "p1~1" || res.Working[1] != "p2~1" {
		t.Fatalf("result %+v", res)
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
	if ttl := mr.TTL("friend:rowan:beat"); ttl != 0 {
		t.Fatalf("friend:rowan:beat keeps a TTL of %s; keys do not expire", ttl)
	}
	row := client.HGetAll(ctx, "friend:rowan").Val()
	if row["at"] != "2026-09-26T16:00:00Z" || row["up"] != "1" || row["host"] != "laptop" {
		t.Fatalf("friend:rowan = %v", row)
	}

	// no measurement: the fields are left alone, nothing empty is written
	res, err = life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: "emma", Host: "studio", At: at})
	if err != nil || len(res.Working) != 0 {
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
