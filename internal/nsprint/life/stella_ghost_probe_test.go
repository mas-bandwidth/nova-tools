//go:build functional

package life_test

import (
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

// Review probe: a crashed harness leaves its copy record, but no owner
// process or live session. An independently surviving beat must not turn
// that stale record back into evidence of a live friend and worker.
func TestStellaDetachedBeatWithoutLiveOwner(t *testing.T) {
	t.Parallel()
	st, c := rdRedis(t)
	ctx := context.Background()
	const friend = "stella-probe"
	const id = "ghost~1"
	c.ZAdd(ctx, "friend:"+friend+":cards:working", redis.Z{Score: 1, Member: id})
	c.HSet(ctx, "task:"+id, "consumer", "friend:"+friend, "where", "working", "lease_until", "1", "child", "dead-child", "harness", "dead-session")
	now := time.Unix(1790448000, 0)
	loop := life.BeatLoop{Lease: life.StoreLease{Client: c, Friend: friend, Me: life.LoopHolder{Token: "surviving-daemon", Host: "fixture", PID: 7}}, Friend: friend, Tick: func(ctx context.Context, at time.Time) (int, error) {
		r, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: friend, Host: "fixture", At: at})
		return r.Working, err
	}}
	for i := 0; i < 3; i++ {
		done, why, err := loop.Step(ctx, now.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("tick=%d done=%v why=%q lease=%s presence_at=%s", i, done, why, c.HGet(ctx, "task:"+id, "lease_until").Val(), c.HGet(ctx, "friend:"+friend+":beat", "at").Val())
		if c.HGet(ctx, "task:"+id, "lease_until").Val() != "1" || c.HExists(ctx, "friend:"+friend+":beat", "at").Val() {
			t.Fatal("unobserved owner renewed lease or presence")
		}
		if done {
			return
		}
	}
	t.Fatal("detached daemon renewed an expired copy and friend presence with no live owner evidence; still running after three ticks")
}
