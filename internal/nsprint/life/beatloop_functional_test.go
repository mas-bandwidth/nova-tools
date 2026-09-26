//go:build functional

package life_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/redis/go-redis/v9"
)

// TestBeatLoopWithFriendBeat (seat-keeps-beat) on a throwaway store: the
// loop's tick is the real friend beat; while the friend holds a working
// copy each step renews the copy's lease and writes the beat's models
// (the copies' models, distinct, in set order); once it holds none the
// models field goes and two idle steps release friend:<f>:beatloop.
func TestBeatLoopWithFriendBeat(t *testing.T) {
	t.Parallel()
	st, client := rdRedis(t)
	ctx := context.Background()
	for i, id := range []string{"p1~1", "p2~1", "p3~1"} {
		client.ZAdd(ctx, "friend:rowan:cards:working", redis.Z{Score: float64(i + 1), Member: id})
		client.HSet(ctx, "task:"+id, "consumer", "friend:rowan", "where", "working", "lease_until", "1")
	}
	client.HSet(ctx, "task:p1~1", "model", "opus-5.5")
	client.HSet(ctx, "task:p3~1", "model", "sonnet-5")
	client.HSet(ctx, "task:p2~1", "model", "opus-5.5")
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	if ok, err := life.ClaimBeatLoop(ctx, client, "rowan", "tok"); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	l := &life.BeatLoop{Client: client, Friend: "rowan", Token: "tok",
		Tick: func(ctx context.Context, at time.Time) (int, error) {
			res, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: "rowan", Host: "laptop", At: at})
			return res.Working, err
		}}
	if done, why, err := l.Step(ctx, now); done || err != nil {
		t.Fatalf("step with copies: done=%v why=%q err=%v", done, why, err)
	}
	beat := client.HGetAll(ctx, "friend:rowan:beat").Val()
	if beat["models"] != "opus-5.5,sonnet-5" || beat["at"] == "" || client.HGet(ctx, "task:p1~1", "lease_until").Val() == "1" {
		t.Fatalf("beat %v, lease %s", beat, client.HGet(ctx, "task:p1~1", "lease_until").Val())
	}
	client.Del(ctx, "friend:rowan:cards:working")
	if done, why, err := l.Step(ctx, now.Add(time.Second)); done || err != nil {
		t.Fatalf("first idle step: done=%v why=%q err=%v", done, why, err)
	}
	if client.HExists(ctx, "friend:rowan:beat", "models").Val() {
		t.Fatal("models kept after the copies left working")
	}
	done, why, err := l.Step(ctx, now.Add(2*time.Second))
	if err != nil || !done || !strings.HasPrefix(why, "IDLE ") || client.Exists(ctx, life.BeatLoopKey("rowan")).Val() != 0 {
		t.Fatalf("second idle step: done=%v why=%q err=%v, lease %d", done, why, err, client.Exists(ctx, life.BeatLoopKey("rowan")).Val())
	}
}
