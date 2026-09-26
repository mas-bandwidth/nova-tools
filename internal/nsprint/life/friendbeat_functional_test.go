//go:build functional

package life_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// A beat renews only process owners observed alive. The record keeps its
// existing session, and a daemon with no owner cannot fabricate presence.
func TestFriendBeatRenewsObservedOwnersAndPreservesPresence(t *testing.T) {
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
		client.HSet(ctx, "task:"+id, "consumer", "friend:rowan", "where", "working", "lease_until", time.Now().Add(time.Minute).UnixMilli(), "token", id+"-token")
		host, _ := os.Hostname()
		sample := life.ProbeProcess(os.Getpid())
		if sample.Err != nil {
			t.Fatal(sample.Err)
		}
		if err := taskcard.BindOwner(ctx, client, taskcard.Consumer{Kind: "friend", Name: "rowan"}, id, id+"-token", taskcard.ProcessOwner{Host: host, PID: os.Getpid(), Start: sample.Start}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

	log := &cmdLog{}
	client.AddHook(log)
	res, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: "Rowan", Host: "laptop", Load1: "0.50", NCPU: 8, CPU: "12.5", At: at})
	if err != nil {
		t.Fatal(err)
	}
	if res.Friend != "rowan" || res.AtMS != at.UnixMilli() || res.Working != 2 || res.LeaseUntil <= time.Now().Add(time.Minute).UnixMilli() {
		t.Fatalf("result %+v", res)
	}
	if log.singles != 1 || log.pipelines != 3 {
		t.Fatalf("beat uses %d single calls + %d pipelines; want four round trips independent of owner count", log.singles, log.pipelines)
	}
	for _, id := range []string{"p1~1", "p2~1"} {
		got, err := client.HGet(ctx, "task:"+id, "lease_until").Int64()
		if err != nil || got <= time.Now().Add(time.Minute).UnixMilli() || got > res.LeaseUntil {
			t.Fatalf("task:%s lease=%d max=%d error=%v", id, got, res.LeaseUntil, err)
		}
	}
	beat := client.HGetAll(ctx, "friend:rowan:beat").Val()
	want := map[string]string{"session": "s1", "host": "laptop", "at": strconv.FormatInt(at.UnixMilli(), 10),
		"harness": life.FriendBeatHarness, "models": "", "load1": "0.50", "ncpu": "8", "cpu": "12.5"}
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
	if beat := client.HGetAll(ctx, "friend:emma:beat").Val(); len(beat) != 0 {
		t.Fatalf("no observed owner fabricated presence: %v", beat)
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
