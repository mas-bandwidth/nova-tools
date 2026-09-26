//go:build functional

package deal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestRedisSourceMaxSessions reads cfg:deal max_sessions in the first round;
// unset, not a positive number, or denied by the ACL, it reads 0 and the read
// still succeeds.
func TestRedisSourceMaxSessions(t *testing.T) {
	t.Parallel()

	c := throwawayRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "ctl-a")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "2")

	read := func(c *redis.Client) (int, error) {
		in, err := RedisSource{Client: c}.Read(ctx)
		return in.MaxSessions, err
	}
	if n, err := read(c); err != nil || n != 0 {
		t.Fatalf("unset: %d %v, want 0", n, err)
	}
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "3")
	if n, err := read(c); err != nil || n != 3 {
		t.Fatalf("set 3: %d %v", n, err)
	}
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "lots")
	if n, err := read(c); err != nil || n != 0 {
		t.Fatalf("garbage: %d %v, want 0", n, err)
	}
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "3")
	// A user whose ACL does not cover cfg:*: the read succeeds, default.
	if err := c.Do(ctx, "ACL", "SETUSER", "nocfg", "on", ">pw-3706", "~bench*", "~sprint*", "~s:*", "+@all").Err(); err != nil {
		t.Fatal(err)
	}
	limited := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: "nocfg", Password: "pw-3706"})
	t.Cleanup(func() { _ = limited.Close() })
	if err := limited.HGet(ctx, MaxSessionsKey, "max_sessions").Err(); err == nil || errors.Is(err, redis.Nil) {
		t.Fatalf("the ACL fixture reads cfg:deal (%v): vacuous", err)
	}
	in, err := RedisSource{Client: limited}.Read(ctx)
	if err != nil || in.MaxSessions != 0 || len(in.Benches) != 1 {
		t.Fatalf("ACL without cfg:*: max %d benches %d err %v, want 0, 1, nil", in.MaxSessions, len(in.Benches), err)
	}
}

// TestRedisSourceRoundErrorsStillFail: forgiving cfg:deal's own error does
// not hide any other command's. A user that can read cfg:deal and the
// registries but not the pools fails the read with the pool round's NOPERM,
// one that cannot read the registries fails the first round, and a lost
// connection fails the read; none returns an empty Input as if there were
// nothing to deal.
func TestRedisSourceRoundErrorsStillFail(t *testing.T) {
	t.Parallel()

	c := throwawayRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "ctl-a")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "2")
	c.SAdd(ctx, "sprints", parallelSprint)
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: parallelSprint})
	c.HSet(ctx, "s:"+parallelSprint, "status", "open")
	c.ZAdd(ctx, "s:"+parallelSprint+":pool", redis.Z{Score: 1, Member: "card-00"})
	c.HSet(ctx, "s:"+parallelSprint+":card:card-00", "state", "queued")
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "3")

	user := func(name string, keys ...string) *redis.Client {
		t.Helper()
		args := []any{"ACL", "SETUSER", name, "on", ">pw-3706"}
		for _, k := range keys {
			args = append(args, "~"+k)
		}
		args = append(args, "+@all")
		if err := c.Do(ctx, args...).Err(); err != nil {
			t.Fatal(err)
		}
		u := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: name, Password: "pw-3706"})
		t.Cleanup(func() { _ = u.Close() })
		return u
	}
	// Every key but the pools: the pool round is refused.
	nopool := user("nopool", "cfg:*", "bench*", "sprint*", "s:"+parallelSprint, "s:"+parallelSprint+":policy",
		"s:"+parallelSprint+":backpressure", "s:"+parallelSprint+":pitstop", "s:"+parallelSprint+":card:*", "s:"+parallelSprint+":waiting")
	if err := nopool.ZCard(ctx, "s:"+parallelSprint+":pool").Err(); err == nil {
		t.Fatal("the ACL fixture reads the pool: vacuous")
	}
	if in, err := (RedisSource{Client: nopool}).Read(ctx); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("pool round refused: err %v input %+v, want the NOPERM", err, in)
	}
	// cfg:deal readable, the registries not: the first round fails even
	// though max_sessions read fine.
	noreg := user("noreg", "cfg:*", "s:*")
	if in, err := (RedisSource{Client: noreg}).Read(ctx); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("registry round refused: err %v input %+v, want the NOPERM", err, in)
	}
	// A lost connection.
	gone := redis.NewClient(&redis.Options{Addr: c.Options().Addr})
	_ = gone.Close()
	if _, err := (RedisSource{Client: gone}).Read(ctx); err == nil {
		t.Fatal("a closed client read an Input, want the error")
	}
	// The full user still reads it all, max_sessions included.
	in, err := RedisSource{Client: c}.Read(ctx)
	if err != nil || in.MaxSessions != 3 || len(in.Sprints) != 1 || len(in.Sprints[0].Pool) != 1 {
		t.Fatalf("full read: %+v %v", in, err)
	}
}
