//go:build functional

package capacity_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
)

// TestBenchSlotsFollowAwakeFriends is the 2026-09-27 rule at run time
// (TM.bench_slots, 02_card_move.lua): a bench's slots are its share capped
// at the machine ceiling less the slots of the friends awake on that
// machine, read at every work. A friend is awake while her beat is within
// TM.LIVE_MS and no friend:<f>:down marker is set; the beats here are
// written in the past, never waited for.
func TestBenchSlotsFollowAwakeFriends(t *testing.T) {
	t.Parallel()

	st, c := redisControl(t)
	ctx := context.Background()
	if _, err := capacity.SetMachineBudget(ctx, st, "m", 128, 32, 192, 0, 0, "test", ""); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "friends", "f1", "f2")
	for _, f := range []string{"f1", "f2"} {
		if _, err := capacity.SetFriend(ctx, st, f, "m", 32, "test", ""); err != nil {
			t.Fatalf("friend %s 32: %v", f, err)
		}
	}
	// the bench's share is the whole machine: allowed beside the friends'
	// 64 (the share is not in the friends' sum)
	if _, err := capacity.SetBenchWith(ctx, st, "m", "m", 128, "test", "", capacity.DesiredOpts{}); err != nil {
		t.Fatalf("bench share 128 beside friends of 64: %v", err)
	}
	// a friend over the friends' sum is still refused
	if _, err := capacity.SetFriend(ctx, st, "f3", "m", 65, "test", ""); err == nil || !strings.Contains(err.Error(), "CEILING m 129/128") {
		t.Fatalf("friend f3 65 beside 64: %v; want CEILING m 129/128", err)
	}

	free := func(want int, why string) {
		t.Helper()
		reply, err := c.FCall(ctx, "ns_cm_work", nil, "bench:m", "test", "0").Result()
		if err != nil {
			t.Fatalf("%s: ns_cm_work: %v", why, err)
		}
		words, _ := reply.([]interface{})
		if len(words) < 3 || fmt.Sprint(words[0]) != "WORKED" {
			t.Fatalf("%s: ns_cm_work reply %v", why, reply)
		}
		if got := fmt.Sprint(words[2]); got != strconv.Itoa(want) {
			t.Fatalf("%s: bench:m free=%s, want %d", why, got, want)
		}
	}
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	ms := now.UnixMilli()
	beat := func(f string, at int64) {
		c.HSet(ctx, "friend:"+f+":beat", "at", strconv.FormatInt(at, 10), "host", "m")
	}

	free(128, "nobody awake: the swarm has the machine")
	beat("f1", ms)
	free(96, "f1 here: her 32 come off")
	beat("f2", ms)
	free(64, "f1 and f2 here")
	beat("f1", ms-120000)
	free(96, "f1's beat is two minutes old: hers are the swarm's again")
	c.Set(ctx, "friend:f2:down", "away", 0)
	free(128, "f2 away (the down marker): hers too")
	c.Del(ctx, "friend:f2:down")
	free(96, "f2 back: the marker gone, her beat still fresh (f1 still stale)")
	// a share under the remainder is the share
	if _, err := capacity.SetBenchWith(ctx, st, "m", "m", 40, "test", "", capacity.DesiredOpts{}); err != nil {
		t.Fatal(err)
	}
	free(40, "share 40 under the remainder 64")
	// the ceiling may be lowered under a bench's share (capped live) but not
	// under the friends' sum: the Go door (SetMachineBudget) and the Lua
	// door (ns_capacity_machine) agree
	if _, err := capacity.SetMachineBudget(ctx, st, "m", 100, 32, 192, 0, 0, "test", ""); err != nil {
		t.Fatalf("ceiling 100 under the bench share 128 (capped live): %v", err)
	}
	free(40, "ceiling 100: the share 40 still under the remainder")
	if _, err := capacity.SetMachineBudget(ctx, st, "m", 63, 32, 192, 0, 0, "test", ""); err == nil || !strings.Contains(err.Error(), "CEILING m 64/63") {
		t.Fatalf("ceiling 63 under the friends' 64: %v; want CEILING m 64/63", err)
	}
	if _, err := capacity.SetMachineBudget(ctx, st, "m", 128, 32, 192, 0, 0, "test", ""); err != nil {
		t.Fatal(err)
	}
	// a friend on another machine is not charged here
	c.SAdd(ctx, "friends", "g")
	if _, err := capacity.SetMachineBudget(ctx, st, "n", 64, 8, 32, 0, 0, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.SetFriend(ctx, st, "g", "n", 64, "test", ""); err != nil {
		t.Fatal(err)
	}
	beat("g", ms)
	free(40, "g awake on n charges n, not m")
}
