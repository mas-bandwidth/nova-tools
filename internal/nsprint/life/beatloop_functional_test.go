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
// loop's tick is the real friend beat and its lease the library's
// (life.StoreLease); while the friend holds a working copy each step renews
// the copy's lease and writes the beat's models (the copies' models,
// distinct, in set order); once it holds none the models field goes and two
// idle steps release friend:<f>:beatloop.
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
	me := life.LoopHolder{Token: "tok", Host: "laptop", PID: 7}
	if ok, _, err := life.ClaimBeatLoop(ctx, client, "rowan", me, ""); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	l := &life.BeatLoop{Lease: life.StoreLease{Client: client, Friend: "rowan", Me: me}, Friend: "rowan",
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

// TestBeatLoopLeaseFunctions (seat-keeps-beat) on a throwaway store: the
// lease's three functions. A claim takes a free lease (a hash: token, host,
// pid, PEXPIRE BeatLoopLease) and reports a held one's holder; renew by the
// holder extends it and writes its pid, by another token is refused; a
// claim naming the holder's token as dead takes it over, naming any other
// leaves it; release by another token leaves it, by the holder deletes it.
func TestBeatLoopLeaseFunctions(t *testing.T) {
	t.Parallel()
	_, c := rdRedis(t)
	ctx := context.Background()
	key := life.BeatLoopKey("Rowan")
	a := life.LoopHolder{Token: "a", Host: "studio"}
	b := life.LoopHolder{Token: "b", Host: "studio", PID: 99}
	if took, _, err := life.ClaimBeatLoop(ctx, c, "Rowan", a, ""); !took || err != nil {
		t.Fatalf("claim of a free lease: %v %v", took, err)
	}
	if ttl := c.PTTL(ctx, key).Val(); ttl <= 0 || ttl > life.BeatLoopLease {
		t.Fatalf("lease pttl %s, want (0, %s]", ttl, life.BeatLoopLease)
	}
	a.PID = 4242
	if ok, err := life.RenewBeatLoop(ctx, c, "rowan", a); !ok || err != nil {
		t.Fatalf("holder's renew: %v %v", ok, err)
	}
	took, held, err := life.ClaimBeatLoop(ctx, c, "rowan", b, "")
	if took || err != nil || held != a {
		t.Fatalf("claim of a held lease: took=%v held=%+v %v, want %+v", took, held, err, a)
	}
	if ok, err := life.RenewBeatLoop(ctx, c, "rowan", b); ok || err != nil {
		t.Fatalf("another token's renew: %v %v", ok, err)
	}
	if took, _, err := life.ClaimBeatLoop(ctx, c, "rowan", b, "not-a"); took || err != nil {
		t.Fatalf("claim naming another dead token: %v %v", took, err)
	}
	if err := life.ReleaseBeatLoop(ctx, c, "rowan", "b"); err != nil || c.HGet(ctx, key, "token").Val() != "a" {
		t.Fatalf("another token's release: %v, lease %v", err, c.HGetAll(ctx, key).Val())
	}
	if took, _, err := life.ClaimBeatLoop(ctx, c, "rowan", b, "a"); !took || err != nil {
		t.Fatalf("claim naming the holder dead: %v %v", took, err)
	}
	if got := c.HGetAll(ctx, key).Val(); got["token"] != "b" || got["pid"] != "99" || got["host"] != "studio" {
		t.Fatalf("lease after the takeover %v", got)
	}
	if err := life.ReleaseBeatLoop(ctx, c, "rowan", "b"); err != nil || c.Exists(ctx, key).Val() != 0 {
		t.Fatalf("holder's release: %v, exists %d", err, c.Exists(ctx, key).Val())
	}
	if ok, err := life.RenewBeatLoop(ctx, c, "rowan", b); !ok || err != nil || c.HGet(ctx, key, "token").Val() != "b" {
		t.Fatalf("renew of a lapsed lease takes it: %v %v", ok, err)
	}
}
