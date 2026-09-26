package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// beatStarts records every beat loop a verb in this test binary starts,
// keyed by the loop's --redis: the binary is not nova-sprint, so nothing is
// ever started for real.
var beatStarts = struct {
	sync.Mutex
	by map[string][][]string
}{by: map[string][][]string{}}

func init() {
	friendBeatStart = func(argv []string) (int, error) {
		addr := ""
		if i := slices.Index(argv, "--redis"); i >= 0 && i+1 < len(argv) {
			addr = argv[i+1]
		}
		beatStarts.Lock()
		defer beatStarts.Unlock()
		beatStarts.by[addr] = append(beatStarts.by[addr], argv)
		return 4242, nil
	}
}

// startsFor is the loops started with --redis addr.
func startsFor(addr string) [][]string {
	beatStarts.Lock()
	defer beatStarts.Unlock()
	return slices.Clone(beatStarts.by[addr])
}

// TestEnsureFriendBeatStartsOneLoop (seat-keeps-beat, invariant A): a
// friend holding a working copy with no loop's lease gets exactly one loop
// started (friend beat --loop --lease <the token the verb claimed>); the
// next verb finds the lease and starts none; a friend holding nothing
// starts none; a start that fails releases the claim; a bench starts none.
func TestEnsureFriendBeatStartsOneLoop(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	k, _ := taskcard.ParseConsumer("friend:rowan")
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	var calls [][]string
	start := func(argv []string) (int, error) { calls = append(calls, argv); return 99, nil }

	if line, err := ensureFriendBeat(ctx, c, k, "", start, now); err != nil || line != "" || len(calls) != 0 {
		t.Fatalf("no working copy: %q %v, %d starts", line, err, len(calls))
	}
	c.ZAdd(ctx, k.Key("working"), redis.Z{Score: 1, Member: "console-grammar~1"})
	line, err := ensureFriendBeat(ctx, c, k, "127.0.0.1:1", start, now)
	if err != nil || line != "BEATLOOP as=friend:rowan started pid=99 working=1" || len(calls) != 1 {
		t.Fatalf("first verb: %q %v, %d starts", line, err, len(calls))
	}
	token := c.Get(ctx, life.BeatLoopKey("rowan")).Val()
	want := []string{"friend", "beat", "--as", "friend:rowan", "--loop", "--lease", token, "--redis", "127.0.0.1:1"}
	if token == "" || !slices.Equal(calls[0][1:], want) {
		t.Fatalf("started %v with lease %q; want <exe> %v", calls[0], token, want)
	}
	if ttl := mr.TTL(life.BeatLoopKey("rowan")); ttl != life.BeatLoopLease {
		t.Fatalf("lease ttl %s, want %s", ttl, life.BeatLoopLease)
	}
	line, err = ensureFriendBeat(ctx, c, k, "", start, now.Add(time.Second))
	if err != nil || line != "BEATLOOP as=friend:rowan running working=1" || len(calls) != 1 {
		t.Fatalf("second verb: %q %v, %d starts", line, err, len(calls))
	}

	mr.Del(life.BeatLoopKey("rowan"))
	failing := func([]string) (int, error) { return 0, errors.New("no fork") }
	if _, err := ensureFriendBeat(ctx, c, k, "", failing, now); err == nil || !strings.Contains(err.Error(), "no fork") {
		t.Fatalf("a failed start: %v", err)
	}
	if mr.Exists(life.BeatLoopKey("rowan")) {
		t.Fatal("a failed start left its claim on the lease")
	}
	b, _ := taskcard.ParseConsumer("bench:studio")
	c.ZAdd(ctx, b.Key("working"), redis.Z{Score: 1, Member: "x~1"})
	if line, err := ensureFriendBeat(ctx, c, b, "", start, now); err != nil || line != "" || len(calls) != 1 {
		t.Fatalf("a bench: %q %v, %d starts", line, err, len(calls))
	}
}

// TestFriendPullAndWorkWhoFlags: one word each for --model, --harness and
// --child on friend pull and card work, refused before any store is
// touched; --lease wants --loop and --loop is not --once.
func TestFriendPullAndWorkWhoFlags(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"friend", "pull", "--as", "friend:rowan", "--model", "opus 5.5", "--redis", "127.0.0.1:1"},
		{"card", "work", "--as", "friend:rowan", "--fill", "--child", "a b", "--redis", "127.0.0.1:1"},
		{"friend", "beat", "--as", "friend:rowan", "--lease", "t", "--redis", "127.0.0.1:1"},
		{"friend", "beat", "--as", "friend:rowan", "--loop", "--once", "--redis", "127.0.0.1:1"},
	} {
		code, out, errOut := runSprint(args...)
		if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint "+args[0]+" "+args[1]+": ") {
			t.Fatalf("%v: exit %d %q %q", args, code, out, errOut)
		}
	}
}
